package proxy

import "embed"

//go:embed html
var htmlFiles embed.FS

//go:embed files/*
var files embed.FS

func getHTMLFile(path string) ([]byte, error) {
	return htmlFiles.ReadFile("html/" + path)
}
