package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const placeholderHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>LLMBench</title>
<style>body{font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem;color:#222}
code{background:#f2f2f2;padding:.1rem .3rem;border-radius:3px}</style></head>
<body><h1>LLMBench</h1>
<p>The API is running, but this binary was built without the web UI.</p>
<p>Build it with <code>make build</code> (or <code>make web</code> before <code>go build</code>).</p>
<p>API health: <a href="/api/v1/health">/api/v1/health</a></p>
</body></html>`

// spaHandler serves static files from files and falls back to index.html for
// client-side routes.
func spaHandler(files fs.FS) http.HandlerFunc {
	var index []byte
	if files != nil {
		index, _ = fs.ReadFile(files, "index.html")
	}
	if index == nil {
		index = []byte(placeholderHTML)
	}
	var fileServer http.Handler
	if files != nil {
		fileServer = http.FileServerFS(files)
	}

	serveIndex := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" || fileServer == nil {
			serveIndex(w)
			return
		}
		if st, err := fs.Stat(files, name); err == nil && !st.IsDir() {
			if strings.HasPrefix(name, "_app/immutable/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w)
	}
}
