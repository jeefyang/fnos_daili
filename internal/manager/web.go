package manager

import (
	"embed"
	"io/fs"
)

//go:embed web
var webFiles embed.FS

// UI returns the embedded web UI rooted at index.html.
func UI() fs.FS {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	return sub
}

// UIHTML returns the index page with its CSRF placeholder intact.
func UIHTML() string {
	b, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		panic(err)
	}
	return string(b)
}
