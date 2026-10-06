// Package sourcefile reads domain and world-state files the way the original
// std::ifstream-based readers do.
package sourcefile

import (
	"io"
	"os"
)

// Read returns the bytes of path and true when the path can be opened. Like
// std::ifstream on Linux, a path that opens but cannot be read (a directory)
// yields empty text rather than an error.
func Read(path string) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	data, _ := io.ReadAll(file)
	return string(data), true
}
