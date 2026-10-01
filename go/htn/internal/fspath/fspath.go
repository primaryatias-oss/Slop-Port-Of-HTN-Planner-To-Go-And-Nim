// Package fspath reproduces the std::filesystem path operations the original
// tooling relies on (libstdc++ semantics on Linux): weakly_canonical,
// lexically_normal and absolute.
package fspath

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// status reports whether path exists and whether its status is known (a
// missing path or a non-directory prefix is known not to exist).
func status(path string) (exists, known bool) {
	_, err := os.Stat(path)
	if err == nil {
		return true, true
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, true
	}
	return false, false
}

// canonical resolves an existing path to an absolute path without symbolic
// links or dot elements (std::filesystem::canonical).
func canonical(path string) (string, bool) {
	if !strings.HasPrefix(path, "/") {
		cwd, err := syscall.Getwd()
		if err != nil {
			return "", false
		}
		path = Append(cwd, path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", false
	}
	return resolved, true
}

// elements splits a path like std::filesystem::path iteration: the root
// directory, then each filename, then an empty filename for a trailing
// separator.
func elements(path string) []string {
	var result []string
	if strings.HasPrefix(path, "/") {
		result = append(result, "/")
	}
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			result = append(result, part)
		}
	}
	if strings.HasSuffix(path, "/") && strings.Trim(path, "/") != "" {
		result = append(result, "")
	}
	return result
}

// Append joins two paths like std::filesystem::path::operator/=.
func Append(base, element string) string {
	if strings.HasPrefix(element, "/") || base == "" {
		return element
	}
	if strings.HasSuffix(base, "/") {
		return base + element
	}
	return base + "/" + element
}

// WeaklyCanonical canonicalizes the longest existing prefix of path and
// appends the remaining elements in normal form. It returns false when a
// status query fails for a reason other than a missing file.
func WeaklyCanonical(path string) (string, bool) {
	if exists, known := status(path); exists {
		return canonical(path)
	} else if !known {
		return "", false
	}
	parts := elements(path)
	result := ""
	index := 0
	for ; index < len(parts); index++ {
		candidate := Append(result, parts[index])
		exists, known := status(candidate)
		if !exists {
			if !known {
				return "", false
			}
			break
		}
		result = candidate
	}
	if result != "" {
		resolved, ok := canonical(result)
		if !ok {
			return "", false
		}
		result = resolved
	}
	for ; index < len(parts); index++ {
		result = Append(result, parts[index])
	}
	return LexicallyNormal(result), true
}

// LexicallyNormal mirrors std::filesystem::path::lexically_normal.
func LexicallyNormal(path string) string {
	if path == "" {
		return ""
	}
	absolute := strings.HasPrefix(path, "/")
	var stack []string
	trailing := false
	tokens := strings.Split(path, "/")
	for _, token := range tokens {
		switch token {
		case "":
			continue
		case ".":
			trailing = true
		case "..":
			if len(stack) > 0 && stack[len(stack)-1] != ".." {
				stack = stack[:len(stack)-1]
				trailing = true
			} else if len(stack) == 0 && absolute {
				trailing = true
			} else {
				stack = append(stack, "..")
				trailing = false
			}
		default:
			stack = append(stack, token)
			trailing = false
		}
	}
	if strings.HasSuffix(path, "/") {
		trailing = true
	}
	if len(stack) == 0 {
		if absolute {
			return "/"
		}
		return "."
	}
	result := strings.Join(stack, "/")
	if absolute {
		result = "/" + result
	}
	if trailing && stack[len(stack)-1] != ".." {
		result += "/"
	}
	return result
}

// Key returns weakly_canonical(path), or lexically_normal(path) when that
// fails (the key used by the original document stores).
func Key(path string) string {
	if canonicalPath, ok := WeaklyCanonical(path); ok {
		return canonicalPath
	}
	return LexicallyNormal(path)
}

// Same compares two paths by Key.
func Same(a, b string) bool { return Key(a) == Key(b) }

// Absolute mirrors std::filesystem::absolute: relative paths are appended
// to the current directory without normalization. It returns false for an
// empty path.
func Absolute(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if strings.HasPrefix(path, "/") {
		return path, true
	}
	// getcwd(3), like std::filesystem::current_path (os.Getwd may return a
	// logical $PWD instead).
	cwd, err := syscall.Getwd()
	if err != nil {
		return "", false
	}
	return Append(cwd, path), true
}
