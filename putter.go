// Simple HTTP server for the TiddlyWiki PUT saver

package main

import (
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	fileServer := http.FileServer(http.Dir("."))
	handle := func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			fileServer.ServeHTTP(w, r)
		case http.MethodOptions:
			handleOptions(w, r)
		case http.MethodPut:
			handlePut(w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}

	http.HandleFunc("/", handle)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// handleOptions responds to an OPTIONS request to signal to TiddlyWiki that
// the server accepts PUT requests. This enables the PUT saver.
func handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Dav", "putter")
	w.WriteHeader(http.StatusOK)
}

// handlePut receives a new version of the wiki, archives the live version,
// and replaces it with the uploaded version.
func handlePut(w http.ResponseWriter, r *http.Request) {
	log.Println("Receiving TiddlyWiki PUT request...")
	f, err := ioutil.TempFile(os.TempDir(), "tiddlywiki-upload-*.html")
	if err != nil {
		log.Printf("failed to open temporary file for upload: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()

	written, err := io.Copy(f, r.Body)
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

	err = archiveWiki()
	if err != nil {
		log.Printf("failed to archive wiki: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = os.Rename(f.Name(), "index.html")
	if err != nil {
		log.Printf("failed replace live wiki: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	log.Println("Updated TiddlyWiki saved successfully")
}

// archiveWiki copies the live version of the wiki into the archive directory.
func archiveWiki() (err error) {
	os.Mkdir("old", 755)

	src, err := os.Open("index.html")
	if err != nil {
		return
	}
	defer src.Close()

	t := time.Now().UTC()
	filename := "old/" + t.Format("2006-01-02-15-04-05.000") + ".html"
	dst, err := os.Create(filename)
	if err != nil {
		return
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	log.Printf("archived wiki to %s", dst.Name())

	return
}
