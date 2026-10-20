package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// scriptPrefix is the one route family that serves this interface's own scripts. The CSP allows script-src
// 'self', so everything under it is code the browser will run: only files embedded in the binary are served.
const scriptPrefix = "/static/js/"

//go:embed static/*.js
var scriptFiles embed.FS

// scripts maps a file name to its content. It is read once from the embedded files, so a request is only
// ever matched against that fixed set and never builds a path.
var scripts = func() map[string][]byte {
	entries, err := fs.ReadDir(scriptFiles, "static")
	if err != nil {
		panic(err)
	}
	m := make(map[string][]byte, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		b, err := scriptFiles.ReadFile("static/" + e.Name())
		if err != nil {
			panic(err)
		}
		m[e.Name()] = b
	}
	return m
}()

// handleScript serves one embedded script by exact name; any other name is 404.
func (s *Server) handleScript(w http.ResponseWriter, r *http.Request) {
	body, ok := scripts[r.PathValue("name")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(body)
}
