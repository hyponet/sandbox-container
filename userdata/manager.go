package userdata

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hyponet/sandbox-container/audit"
)

const DefaultRoot = "/data/users"

// Manager manages user data directories, providing creation, path resolution,
// and validation. It is designed to be imported by both handler and executor packages.
type Manager struct {
	root   string
	inited sync.Map // userID -> struct{}, tracks users whose userdata dir is created
	initFn func(sessionDir, userdataDir string)
}

// NewManager creates a new userdata manager with the given root directory.
func NewManager(root string) *Manager {
	if root == "" {
		root = DefaultRoot
	}
	return &Manager{root: root}
}

// Root returns the userdata directory path for a given user.
func (m *Manager) Root(userID string) string {
	return filepath.Join(m.root, userID)
}

// SetRoot sets the userdata root directory (for testing).
func (m *Manager) SetRoot(path string) {
	m.root = path
}

// SetInitFn sets the callback invoked after userdata directories are set up.
// The callback receives (sessionDir, userdataDir) and is responsible for
// executor-specific setup (e.g. creating symlinks in direct mode).
func (m *Manager) SetInitFn(fn func(sessionDir, userdataDir string)) {
	m.initFn = fn
}

// InitFn returns the init callback (for use by handler layer).
func (m *Manager) InitFn() func(sessionDir, userdataDir string) {
	return m.initFn
}

// Touch creates the userdata directory for a user (idempotent).
// Validates user_id to prevent path traversal before creating the directory.
// If the directory was previously created but later removed, it will be recreated.
func (m *Manager) Touch(userID string) error {
	if userID == "" {
		return nil
	}
	if err := audit.ValidateID(userID); err != nil {
		return fmt.Errorf("invalid user_id: %w", err)
	}
	dir := m.Root(userID)
	// Fast path: check if already initialized and directory still exists.
	if _, loaded := m.inited.Load(userID); loaded {
		if _, err := os.Stat(dir); err == nil {
			return nil
		}
		// Directory was removed; clear cache and recreate.
		m.inited.Delete(userID)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[ERROR] userdata.Touch: failed to create %s: %v", dir, err)
		return err
	}
	m.inited.Store(userID, struct{}{})
	return nil
}

// IsPath checks if a request path targets the userdata directory.
func IsPath(reqPath string) bool {
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath == "/userdata" || strings.HasPrefix(cleanPath, "/userdata/")
}

// ResolvePath resolves a /userdata/... path to the host userdata directory.
func (m *Manager) ResolvePath(userID, reqPath string) (string, error) {
	if userID == "" {
		return "", fmt.Errorf("user_id is required for /userdata/ paths")
	}
	if err := audit.ValidateID(userID); err != nil {
		return "", fmt.Errorf("invalid user_id: %w", err)
	}
	if err := rejectDotDot(reqPath); err != nil {
		return "", err
	}
	cleanPath := cleanRequestPath(reqPath)
	relPath := strings.TrimPrefix(cleanPath, "/userdata")
	relPath = strings.TrimPrefix(relPath, "/")
	return resolveUnder(m.Root(userID), relPath)
}

// rejectDotDot rejects paths containing ".." components.
func rejectDotDot(reqPath string) error {
	for _, component := range strings.Split(reqPath, "/") {
		if component == ".." {
			return fmt.Errorf("path contains invalid component: %s", reqPath)
		}
	}
	return nil
}

// cleanRequestPath cleans a request path and ensures it is absolute.
func cleanRequestPath(reqPath string) string {
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath
}

// resolveUnder resolves reqPath to be within rootDir, with path traversal protection.
func resolveUnder(rootDir, reqPath string) (string, error) {
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
