# Putter

## Overview

Putter is a simple HTTP server for [TiddlyWiki](https://tiddlywiki.com/) that supports the `PUT` saver (`$:/core/modules/savers/put.js`).

When served via Putter, the default behavior of a TiddlyWiki's "save" functionality will be to send a `PUT` request, updating the version on the server. The `ETag` header is used to prevent conflicting saves from overwriting each other.

Be default, Putter serves the `index.html` file from the current directory and archives the previous version of the wiki to `old/` whenever a new version is saved. This behavior is configurable via command line flags.

Note that the entire wiki is re-uploaded with each save. TiddlyWiki's [automatic saving feature](https://tiddlywiki.com/static/AutoSave.html) (`$:/config/AutoSave`) can be disabled to save bandwidth.

## Usage

The following flags are available:

- `--archive`=bool
  - default `true`
  - whether wiki edit history should be preserved in `--archive-dir`
- `--archive-dir` string
  - default `old`
  - directory in which edit history will be preserved
- `--archive-format` string
  - default `2006-01-02-15-04-05.000.html`
  - format of archive filenames
- `--archive-path` string
  - default `/old/`
  - path at which edit history will be served over HTTP
- `--bind` string
  - default `127.0.0.1`
  - interface to which the server will bind
- `--compress`=bool
  - default `true`
  - whether a gzipped version of the wiki should also be served
- `--port` int
  - default `8080`
  - port on which the server will listen
- `--serve-archive`=bool
  - default `true`
  - whether wiki edit history should be served over HTTP at `--archive-path`
- `--wiki` string
  - default `index.html`
  - wiki file to serve
