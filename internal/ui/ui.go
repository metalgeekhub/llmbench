// Package ui embeds the built frontend. `make web` copies web/build into
// dist/; until then only dist/.gitkeep exists and the server shows a
// placeholder page.
package ui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Files returns the frontend file system and whether a built UI is present.
func Files() (fs.FS, bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	_, err = fs.Stat(sub, "index.html")
	return sub, err == nil
}
