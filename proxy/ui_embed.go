package proxy

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed ui_dist
var reactStaticFS embed.FS

// GetReactFS returns the embedded React filesystem
func GetReactFS() (http.FileSystem, error) {
	subFS, err := fs.Sub(reactStaticFS, "ui_dist")
	if err != nil {
		return nil, err
	}
	return http.FS(subFS), nil
}

// GetReactIndexHTML returns the main index.html for the React app
func GetReactIndexHTML() ([]byte, error) {
	return reactStaticFS.ReadFile("ui_dist/index.html")
}
