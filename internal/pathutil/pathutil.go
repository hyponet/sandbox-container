package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RejectDotDot rejects paths containing ".." components.
func RejectDotDot(reqPath string) error {
	for _, component := range strings.Split(reqPath, "/") {
		if component == ".." {
			return fmt.Errorf("path contains invalid component: %s", reqPath)
		}
	}
	return nil
}

// CleanRequestPath cleans a request path and ensures it is absolute.
func CleanRequestPath(reqPath string) string {
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath
}

// ResolveUnder resolves reqPath to be within rootDir, with path traversal protection.
func ResolveUnder(rootDir, reqPath string) (string, error) {
	cleanRoot := filepath.Clean(rootDir)
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	relPath := strings.TrimPrefix(cleanPath, "/")
	realPath := filepath.Join(cleanRoot, relPath)
	realPath = filepath.Clean(realPath)
	if !strings.HasPrefix(realPath+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) && realPath != cleanRoot {
		return "", fmt.Errorf("path escapes directory: %s", reqPath)
	}
	return realPath, nil
}
