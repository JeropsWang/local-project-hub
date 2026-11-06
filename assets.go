//go:build windows && desktop

package main

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var embeddedFrontend embed.FS

func frontendAssets() fs.FS {
	files, err := fs.Sub(embeddedFrontend, "frontend/dist")
	if err != nil {
		panic(err)
	}
	return files
}
