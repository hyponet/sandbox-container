package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	DefaultAgentRoot  = "/data/agents"
	DefaultTTL        = 24 * time.Hour
	CleanupInterval   = 10 * time.Minute
)

// Manager manages session directories with TTL-based cleanup.
type Manager struct {
	root       string
	ttl        time.Duration
	mu         sync.RWMutex
	accessTime map[string]time.Time // "agentID/sessionID" -> last access time
}

// NewManager creates a new session manager.
func NewManager(root string, ttl time.Duration) *Manager {
	if root == "" {
		root = DefaultAgentRoot
	}
	if ttl == 0 {
		ttl = DefaultTTL
	}
	m := &Manager{
		root:       root,
		ttl:        ttl,
		accessTime: make(map[string]time.Time),
	}
	os.MkdirAll(root, 0755)
	go m.cleanupLoop()
	return m
}

func (m *Manager) agentKey(agentID, sessionID string) string {
	return agentID + "/" + sessionID
}

// AgentRoot returns the root directory for a given agent.
func (m *Manager) AgentRoot(agentID string) string {
	return filepath.Join(m.root, agentID)
}

// SessionRoot returns the root directory for a given session.
func (m *Manager) SessionRoot(agentID, sessionID string) string {
	return filepath.Join(m.root, agentID, "sessions", sessionID)
}

// SkillsRoot returns the skills directory for a given agent.
func (m *Manager) SkillsRoot(agentID string) string {
	return filepath.Join(m.root, agentID, "skills")
}

// IsSkillsPath checks if a request path targets the skills directory.
func IsSkillsPath(reqPath string) bool {
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}
	return cleanPath == "/skills" || strings.HasPrefix(cleanPath, "/skills/")
}

// ResolvePath rewrites an absolute path to be within the session directory.
// Paths starting with /skills/ are mapped to the agent's skills directory (read-only).
// The given path must be absolute. Returns the real path, whether it's a skills path, and an error if path escapes.
func (m *Manager) ResolvePath(agentID, sessionID, reqPath string) (string, error) {
	m.Touch(agentID, sessionID)

	// Security: reject any path containing ".." components
	for _, component := range strings.Split(reqPath, "/") {
		if component == ".." {
			return "", fmt.Errorf("path escapes session directory: %s", reqPath)
		}
	}

	// Clean the requested path
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}

	// Check if this is a skills path
	if IsSkillsPath(cleanPath) {
		skillsRoot := filepath.Clean(m.SkillsRoot(agentID))
		relPath := strings.TrimPrefix(cleanPath, "/skills")
		relPath = strings.TrimPrefix(relPath, "/")
		var realPath string
		if relPath == "" {
			realPath = skillsRoot
		} else {
			realPath = filepath.Join(skillsRoot, relPath)
		}
		realPath = filepath.Clean(realPath)
		if !strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) && realPath != skillsRoot {
			return "", fmt.Errorf("path escapes skills directory: %s", reqPath)
		}
		return realPath, nil
	}

	// Regular session path
	sessionRoot := filepath.Clean(m.SessionRoot(agentID, sessionID))
	relPath := strings.TrimPrefix(cleanPath, "/")
	realPath := filepath.Join(sessionRoot, relPath)

	// Double-check: ensure resolved path is within session root
	realPath = filepath.Clean(realPath)
	if !strings.HasPrefix(realPath+string(os.PathSeparator), sessionRoot+string(os.PathSeparator)) && realPath != sessionRoot {
		return "", fmt.Errorf("path escapes session directory: %s", reqPath)
	}

	return realPath, nil
}

// ResolveReadOnlyPath resolves a path and checks if it's in the read-only skills directory.
func (m *Manager) ResolveReadOnlyPath(agentID, sessionID, reqPath string) (string, error) {
	return m.ResolvePath(agentID, sessionID, reqPath)
}

// IsResolvedSkillsPath checks if a resolved absolute path is within an agent's skills directory.
func (m *Manager) IsResolvedSkillsPath(agentID, resolvedPath string) bool {
	skillsRoot := filepath.Clean(m.SkillsRoot(agentID))
	cleanResolved := filepath.Clean(resolvedPath)
	return strings.HasPrefix(cleanResolved+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || cleanResolved == skillsRoot
}

// EnsureDir creates the session directory and any parent directories for a file.
func (m *Manager) EnsureDir(agentID, sessionID, dirPath string) error {
	realPath, err := m.ResolvePath(agentID, sessionID, dirPath)
	if err != nil {
		return err
	}
	return os.MkdirAll(realPath, 0755)
}

// EnsureParentDir creates parent directories for a file path.
func (m *Manager) EnsureParentDir(agentID, sessionID, filePath string) error {
	realPath, err := m.ResolvePath(agentID, sessionID, filePath)
	if err != nil {
		return err
	}
	parent := filepath.Dir(realPath)
	return os.MkdirAll(parent, 0755)
}

// Touch updates the last access time for a session and creates the directory + skills symlink.
func (m *Manager) Touch(agentID, sessionID string) {
	key := m.agentKey(agentID, sessionID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accessTime[key] = time.Now()

	// Ensure session directory exists
	sessionDir := m.SessionRoot(agentID, sessionID)
	os.MkdirAll(sessionDir, 0755)

	// Create skills symlink: <session>/skills -> <agent>/skills
	symlinkPath := filepath.Join(sessionDir, "skills")
	skillsDir := m.SkillsRoot(agentID)

	// Remove existing symlink/file if it exists
	os.Remove(symlinkPath)

	// Ensure skills directory exists
	os.MkdirAll(skillsDir, 0755)

	// Create relative symlink
	relSkills, err := filepath.Rel(sessionDir, skillsDir)
	if err != nil {
		// Fallback to absolute symlink
		relSkills = skillsDir
	}
	os.Symlink(relSkills, symlinkPath)
}

// Exists checks if a session directory exists.
func (m *Manager) Exists(agentID, sessionID string) bool {
	info, err := os.Stat(m.SessionRoot(agentID, sessionID))
	return err == nil && info.IsDir()
}

// cleanupLoop periodically removes expired session directories.
func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		m.cleanup()
	}
}

func (m *Manager) cleanup() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for key, lastAccess := range m.accessTime {
		if now.Sub(lastAccess) > m.ttl {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) == 2 {
				sessionDir := m.SessionRoot(parts[0], parts[1])
				os.RemoveAll(sessionDir)
			}
			delete(m.accessTime, key)
		}
	}
}
