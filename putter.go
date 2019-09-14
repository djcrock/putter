// Simple HTTP server for the TiddlyWiki PUT saver

package main

import (
	"compress/gzip"
	"crypto/md5"
	"encoding/hex"
	"flag"
	"hash"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	headerAcceptEncoding  = "Accept-Encoding"
	headerContentEncoding = "Content-Encoding"
	headerContentLength   = "Content-Length"
	headerDav             = "Dav"
	headerEtag            = "ETag"
	headerIfMatch         = "If-Match"

	encodingGzip = "gzip"

	extensionGzip = ".gz"
)

func main() {
	bind := flag.String("bind", "127.0.0.1", "interface to which the server will bind")
	port := flag.Int("port", 8080, "port on which the server will listen")
	wiki := flag.String("wiki", "index.html", "wiki file to serve")
	archive := flag.Bool("archive", true, "whether wiki edit history should be preserved in --archive-dir")
	archiveDir := flag.String("archive-dir", "old", "directory in which edit history will be preserved")
	archiveFormat := flag.String("archive-format", "2006-01-02-15-04-05.000.html", "format of archive filenames")
	serveArchive := flag.Bool("serve-archive", true, "whether wiki edit history should be served over HTTP at --archive-path")
	archivePath := flag.String("archive-path", "/old/", "path at which edit history will be served over HTTP")
	compress := flag.Bool("compress", true, "whether a gzipped version of the wiki should also be saved")
	flag.Parse()

	ip := net.ParseIP(*bind)
	if ip == nil {
		log.Fatal("invalid IP address provided to --bind")
	}

	addr := ip.String() + ":" + strconv.Itoa(*port)

	s := newServer(
		*wiki,
		*archiveDir,
		*archiveFormat,
		*archive,
		*compress,
	)
	http.Handle("/", s)
	log.Printf("serving wiki \"%s\" at http://%s/", *wiki, addr)

	if *archive && *serveArchive {
		path := fixPath(*archivePath)
		dir := http.FileServer(http.Dir(*archiveDir))
		dir = whitelistMethods(dir, http.MethodGet, http.MethodHead)
		http.Handle(path, http.StripPrefix(path, dir))
		log.Printf("serving archive \"%s\" at http://%s%s", *archiveDir, addr, path)
	}

	log.Fatal(http.ListenAndServe(addr, nil))
}

// fixPath ensures that the given string begins and ends with '/'
func fixPath(p string) string {
	if p[0] != '/' {
		p = "/" + p
	}
	if p[len(p)-1] != '/' {
		p = p + "/"
	}

	return p
}

// whitelistMethods decorates an http.Handler by only allowing certain methods
func whitelistMethods(h http.Handler, methods ...string) http.Handler {
	allow := make(map[string]bool)
	for _, method := range methods {
		allow[method] = true
	}

	handlerFunc := func(w http.ResponseWriter, r *http.Request) {
		if allow[r.Method] {
			h.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}

	return http.HandlerFunc(handlerFunc)
}

// Server for providing safe concurrent reads and writes to a TiddlyWiki
type Server struct {
	mu             sync.RWMutex // protects the following
	etag           string       // ETag for the live wiki
	fileName       string       // name of the wiki file
	archiveDirName string       // name of the directory to archive to
	archiveFormat  string       // format of archive filenames
	isArchive      bool         // whether archiving should be performed
	isCompress     bool         // whether compression is enabled
}

// newServer creates a new instance of Server, computing the initial ETag.
func newServer(
	fileName, archiveDirName, archiveFormat string,
	isArchive, isCompress bool,
) *Server {
	s := &Server{
		fileName:       fileName,
		archiveDirName: archiveDirName,
		archiveFormat:  archiveFormat,
		isArchive:      isArchive,
		isCompress:     isArchive,
	}
	f, err := os.Open(s.fileName)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	hash := md5.New()
	_, err = io.Copy(hash, f)
	if err != nil {
		log.Fatal(err)
	}

	s.setEtagFromHash(hash)

	err = s.compressWiki()
	if err != nil {
		log.Fatal(err)
	}

	return s
}

// ServeHTTP handles all requests for the live wiki
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodHead:
		s.handleHead(w, r)
	case http.MethodOptions:
		s.handleOptions(w, r)
	case http.MethodGet:
		s.handleGet(w, r)
	case http.MethodPut:
		s.handlePut(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleHead responds to a HEAD request by responding with ETag information.
// This allows the PUT saver to get an initial ETag value, since it doesn't have
// access to the value from the initial GET request.
func (s *Server) handleHead(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	w.Header().Set(headerEtag, s.etag)
	w.WriteHeader(http.StatusOK)
}

// handleOptions responds to an OPTIONS request to signal to TiddlyWiki that
// the server accepts PUT requests. This enables the PUT saver.
func (s *Server) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(headerDav, "putter")
	w.WriteHeader(http.StatusOK)
}

// handleGet responds to a GET request by serving the wiki.
// A separate handler is used (vs. http.FileServer) to support ETags.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	etag := s.etag
	acceptEncoding := r.Header.Get(headerAcceptEncoding)
	extension := ""
	if s.isCompress && strings.Contains(acceptEncoding, encodingGzip) {
		extension = extensionGzip
		w.Header().Set(headerContentEncoding, encodingGzip)
	}
	f, err := os.Open(s.fileName + extension)
	if err != nil {
		log.Printf("failed to open wiki file to serve: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer f.Close()
	fileInfo, err := f.Stat()
	if err != nil {
		log.Printf("failed to stat wiki file to serve: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	// Now that we have the ETag and file handle, nothing can change under us
	s.mu.RUnlock()

	// http.ServeContent won't automatically add this if Content-Encoding is set
	w.Header().Set(headerContentLength, strconv.FormatInt(fileInfo.Size(), 10))
	w.Header().Set(headerEtag, etag)
	http.ServeContent(w, r, s.fileName, fileInfo.ModTime(), f)
}

// handlePut receives a new version of the wiki, archives the live version,
// and replaces it with the uploaded version.
func (s *Server) handlePut(w http.ResponseWriter, r *http.Request) {
	log.Println("receiving PUT request...")
	f, err := ioutil.TempFile(os.TempDir(), "tiddlywiki-upload-*.html")
	if err != nil {
		log.Printf("failed to open temporary file for upload: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	hash := md5.New()
	written, err := io.Copy(io.MultiWriter(f, hash), r.Body)
	if err != nil {
		log.Printf("failed to save request body: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	log.Printf("received %d bytes", written)

	err = f.Close()
	if err != nil {
		log.Printf("failed to close temporary file: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	etag := r.Header.Get(headerIfMatch)
	if etag != "" && etag != s.etag {
		log.Printf("conflicting ETag (client : %s, server : %s)", etag, s.etag)
		w.WriteHeader(http.StatusPreconditionFailed)
		return
	}

	err = s.archiveWiki()
	if err != nil {
		log.Printf("failed to archive wiki: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = os.Rename(f.Name(), s.fileName)
	if err != nil {
		log.Printf("failed replace live wiki: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = s.compressWiki()
	if err != nil {
		log.Printf("failed compress wiki: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	s.setEtagFromHash(hash)
	w.Header().Set(headerEtag, s.etag)
	w.WriteHeader(http.StatusOK)

	log.Println("wiki saved successfully")
}

// compressWiki creates a compressed version of the wiki.
func (s *Server) compressWiki() (err error) {
	if !s.isCompress {
		return
	}
	log.Println("compressing wiki...")
	src, err := os.Open(s.fileName)
	if err != nil {
		return
	}
	defer src.Close()

	dst, err := os.Create(s.fileName + extensionGzip)
	if err != nil {
		return
	}
	defer dst.Close()

	dstz, err := gzip.NewWriterLevel(dst, gzip.BestCompression)
	if err != nil {
		return
	}
	defer dstz.Close()

	_, err = io.Copy(dstz, src)
	if err != nil {
		return
	}
	log.Println("wiki compressed")

	return
}

// archiveWiki copies the live version of the wiki into the archive directory.
func (s *Server) archiveWiki() (err error) {
	if !s.isArchive {
		return
	}
	os.Mkdir(s.archiveDirName, 755)

	src, err := os.Open(s.fileName)
	if err != nil {
		return
	}
	defer src.Close()

	t := time.Now().UTC()
	filename := s.archiveDirName + "/" + t.Format(s.archiveFormat)
	dst, err := os.Create(filename)
	if err != nil {
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	log.Printf("archived wiki to %s", dst.Name())

	return
}

// setEtagFromHash gets the sum of the hash and sets it as the current ETag
func (s *Server) setEtagFromHash(h hash.Hash) {
	s.etag = "\"" + hex.EncodeToString(h.Sum(nil)) + "\""
}
