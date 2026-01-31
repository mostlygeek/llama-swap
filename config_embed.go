package main

import (
	"bytes"
	_ "embed"
)

//go:embed config.example.yaml
var configExampleYAML []byte

// GetConfigExampleYAML returns the embedded example config file
func GetConfigExampleYAML() []byte {
	return bytes.Clone(configExampleYAML)
}
