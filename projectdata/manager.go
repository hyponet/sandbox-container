package projectdata

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/internal/pathutil"
)

const DefaultRoot = "/data/projects"

// Manager manages project data directories, providing creation, path resolution,
// and validation. It mirrors the userdata.Manager pattern for project-level data sharing.
type Manager struct {
	root   string
	inited sync.Map // projectID -> struct{}, tracks projects whose dir is created
	initFn func(sessionDir, projectdataDir string) error
}

// NewManager creates a new projectdata manager with the given root directory.
func NewManager(root string) *Manager {
	if root == "" {
		root = DefaultRoot
	}
	return &Manager{root: root}
}

// Root returns the projectdata directory path for a given project.
func (m *Manager) Root(projectID string) string {
	return filepath.Join(m.root, projectID)
}

// SetRoot sets the projectdata root directory (for testing).
func (m *Manager) SetRoot(path string) {
	m.root = path
}

// SetInitFn sets the callback invoked after projectdata directories are set up.
// The callback receives (sessionDir, projectdataDir) and is responsible for
// executor-specific setup (e.g. bind mount preparation).
func (m *Manager) SetInitFn(fn func(sessionDir, projectdataDir string) error) {
	m.initFn = fn
}

// InitFn returns the init callback (for use by handler layer).
func (m *Manager) InitFn() func(sessionDir, projectdataDir string) error {
	return m.initFn
}

// Touch creates the projectdata directory for a project (idempotent).
// Validates project_id to prevent path traversal before creating the directory.
// If the directory was previously created but later removed, it will be recreated.
func (m *Manager) Touch(projectID string) error {
	if projectID == "" {
		return nil
	}
	if err := audit.ValidateID(projectID); err != nil {
		return fmt.Errorf("invalid project_id: %w", err)
	}
	dir := m.Root(projectID)
	// Fast path: check if already initialized and directory still exists.
	if _, loaded := m.inited.Load(projectID); loaded {
		if _, err := os.Stat(dir); err == nil {
			return nil
		}
		// Directory was removed; clear cache and recreate.
		m.inited.Delete(projectID)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("[ERROR] projectdata.Touch: failed to create %s: %v", dir, err)
		return err
	}
	m.inited.Store(projectID, struct{}{})
	return nil
}

// IsPath checks if a request path targets the projectdata directory.
func IsPath(reqPath string) bool {
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath == "/projectdata" || strings.HasPrefix(cleanPath, "/projectdata/")
}

// ResolvePath resolves a /projectdata/... path to the host projectdata directory.
func (m *Manager) ResolvePath(projectID, reqPath string) (string, error) {
	if projectID == "" {
		return "", fmt.Errorf("project_id is required for /projectdata/ paths")
	}
	if err := audit.ValidateID(projectID); err != nil {
		return "", fmt.Errorf("invalid project_id: %w", err)
	}
	if err := pathutil.RejectDotDot(reqPath); err != nil {
		return "", err
	}
	cleanPath := pathutil.CleanRequestPath(reqPath)
	relPath := strings.TrimPrefix(cleanPath, "/projectdata")
	relPath = strings.TrimPrefix(relPath, "/")
	return pathutil.ResolveUnder(m.Root(projectID), relPath)
}
