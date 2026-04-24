package session

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hyponet/sandbox-container/audit"
)

const (
	DefaultAgentRoot    = "/data/agents"
	DefaultGlobalSkills = "/data/skills"
	DefaultRegistryRoot = "/data/skill-registry"
	DefaultTTL          = 24 * time.Hour
	CleanupInterval     = 10 * time.Minute
)

// Manager manages session directories with TTL-based cleanup.
type Manager struct {
	root             string
	globalSkillsRoot string
	registryRoot     string
	ttl              time.Duration
	mu               sync.RWMutex
	accessTime       map[string]time.Time // "agentID/sessionID" -> last access time
	auditWriter      *audit.Writer
	workspaceInited  sync.Map // agentID -> struct{}, tracks agents whose workspace is set up
	sessionInit      func(sessionDir, skillsDir string)
}

// SessionEntry represents a session with its last access time.
type SessionEntry struct {
	SessionID  string
	LastAccess time.Time
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
		root:             root,
		globalSkillsRoot: DefaultGlobalSkills,
		registryRoot:     DefaultRegistryRoot,
		ttl:              ttl,
		accessTime:       make(map[string]time.Time),
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		log.Fatalf("[FATAL] failed to create root directory %s: %v", root, err)
	}
	// Registry and globalSkillsRoot directories are created in main.go at startup,
	// or via SetRegistryRoot/SetGlobalSkillsRoot in tests.
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

// WorkspaceRoot returns the persistent workspace directory for a given agent.
func (m *Manager) WorkspaceRoot(agentID string) string {
	return filepath.Join(m.root, agentID, "workspace")
}

// SkillsRoot returns the skills directory for a given agent.
func (m *Manager) SkillsRoot(agentID string) string {
	return filepath.Join(m.root, agentID, "skills")
}

// GlobalSkillsRoot returns the global skills directory.
func (m *Manager) GlobalSkillsRoot() string {
	return m.globalSkillsRoot
}

// GlobalSkillPath returns the path for a specific global skill.
func (m *Manager) GlobalSkillPath(skillID string) string {
	return filepath.Join(m.globalSkillsRoot, skillID)
}

// SetGlobalSkillsRoot sets the global skills root directory (for testing).
func (m *Manager) SetGlobalSkillsRoot(path string) {
	m.globalSkillsRoot = path
	if err := os.MkdirAll(path, 0755); err != nil {
		log.Printf("[ERROR] SetGlobalSkillsRoot: failed to create %s: %v", path, err)
	}
}

// RegistryRoot returns the skill registry root directory.
func (m *Manager) RegistryRoot() string {
	return m.registryRoot
}

// RegistrySkillPath returns the registry directory for a specific skill.
func (m *Manager) RegistrySkillPath(skillID string) string {
	return filepath.Join(m.registryRoot, skillID)
}

// RegistryVersionPath returns the path for a specific version of a skill.
func (m *Manager) RegistryVersionPath(skillID, version string) string {
	return filepath.Join(m.registryRoot, skillID, version)
}

// SetRegistryRoot sets the registry root directory (for testing).
func (m *Manager) SetRegistryRoot(path string) {
	m.registryRoot = path
	if err := os.MkdirAll(path, 0755); err != nil {
		log.Printf("[ERROR] SetRegistryRoot: failed to create %s: %v", path, err)
	}
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

	// Security: reject any path containing ".." components before Clean normalizes them away.
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

// ResolvePathEx resolves a path with optional workspace mode support.
// When agentWorkspace is true, non-skills paths resolve under the agent's workspace directory.
// When false, it delegates to ResolvePath (session-based isolation).
func (m *Manager) ResolvePathEx(agentID, sessionID, reqPath string, agentWorkspace bool) (string, error) {
	if !agentWorkspace {
		return m.ResolvePath(agentID, sessionID, reqPath)
	}

	m.TouchWorkspace(agentID)

	// Security: reject any path containing ".." components before Clean normalizes them away.
	for _, component := range strings.Split(reqPath, "/") {
		if component == ".." {
			return "", fmt.Errorf("path escapes workspace directory: %s", reqPath)
		}
	}

	// Clean the requested path
	cleanPath := filepath.Clean(reqPath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
	}

	// Check if this is a skills path — always resolves to agent skills dir
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

	// Regular workspace path
	wsRoot := filepath.Clean(m.WorkspaceRoot(agentID))
	relPath := strings.TrimPrefix(cleanPath, "/")
	realPath := filepath.Join(wsRoot, relPath)

	// Ensure resolved path is within workspace root
	realPath = filepath.Clean(realPath)
	if !strings.HasPrefix(realPath+string(os.PathSeparator), wsRoot+string(os.PathSeparator)) && realPath != wsRoot {
		return "", fmt.Errorf("path escapes workspace directory: %s", reqPath)
	}

	return realPath, nil
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

// EnsureParentDirEx creates parent directories for a file path with workspace mode support.
func (m *Manager) EnsureParentDirEx(agentID, sessionID, filePath string, agentWorkspace bool) error {
	realPath, err := m.ResolvePathEx(agentID, sessionID, filePath, agentWorkspace)
	if err != nil {
		return err
	}
	parent := filepath.Dir(realPath)
	return os.MkdirAll(parent, 0755)
}

// SetSessionInit sets the callback invoked after session/workspace directories are created.
func (m *Manager) SetSessionInit(fn func(sessionDir, skillsDir string)) {
	m.sessionInit = fn
}

// Touch updates the last access time for a session and creates the directory.
// If a sessionInit callback is set, it is invoked after directory creation.
func (m *Manager) Touch(agentID, sessionID string) {
	key := m.agentKey(agentID, sessionID)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accessTime[key] = time.Now()

	// Ensure session directory exists
	sessionDir := m.SessionRoot(agentID, sessionID)
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		log.Printf("[ERROR] Touch: failed to create session dir %s: %v", sessionDir, err)
	}

	// Ensure skills directory exists
	skillsDir := m.SkillsRoot(agentID)
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		log.Printf("[ERROR] Touch: failed to create skills dir %s: %v", skillsDir, err)
	}

	if m.sessionInit != nil {
		m.sessionInit(sessionDir, skillsDir)
	}
}

// TouchWorkspace creates the workspace directory for an agent.
// Unlike Touch, it does not track TTL-based cleanup.
// It is idempotent: once initialized for an agent, subsequent calls are no-ops.
// If a sessionInit callback is set, it is invoked after directory creation.
func (m *Manager) TouchWorkspace(agentID string) {
	if _, loaded := m.workspaceInited.LoadOrStore(agentID, struct{}{}); loaded {
		return
	}

	wsDir := m.WorkspaceRoot(agentID)
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		log.Printf("[ERROR] TouchWorkspace: failed to create workspace dir %s: %v", wsDir, err)
		m.workspaceInited.Delete(agentID)
		return
	}

	skillsDir := m.SkillsRoot(agentID)
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		log.Printf("[ERROR] TouchWorkspace: failed to create skills dir %s: %v", skillsDir, err)
		m.workspaceInited.Delete(agentID)
		return
	}

	if m.sessionInit != nil {
		m.sessionInit(wsDir, skillsDir)
	}
}

// Exists checks if a session directory exists.
func (m *Manager) Exists(agentID, sessionID string) bool {
	if err := audit.ValidateID(agentID); err != nil {
		return false
	}
	if err := audit.ValidateID(sessionID); err != nil {
		return false
	}
	info, err := os.Stat(m.SessionRoot(agentID, sessionID))
	return err == nil && info.IsDir()
}

// SetAuditWriter injects the audit writer for cleanup coordination.
func (m *Manager) SetAuditWriter(w *audit.Writer) {
	m.auditWriter = w
}

// AuditDir returns the audits directory for a given agent.
func (m *Manager) AuditDir(agentID string) string {
	return filepath.Join(m.root, agentID, "audits")
}

// AuditPath returns the audit log file path for a session.
func (m *Manager) AuditPath(agentID, sessionID string) string {
	return filepath.Join(m.root, agentID, "audits", sessionID+".jsonl")
}

// SyncAudit flushes buffered audit data for a session to disk.
// This should be called before reading the audit file to ensure consistency.
func (m *Manager) SyncAudit(agentID, sessionID string) {
	if m.auditWriter != nil {
		m.auditWriter.SyncSession(agentID, sessionID)
	}
}

// ListSessions returns all sessions for a given agent, merging in-memory and on-disk state.
func (m *Manager) ListSessions(agentID string) ([]SessionEntry, error) {
	if err := audit.ValidateID(agentID); err != nil {
		return nil, fmt.Errorf("invalid agent_id: %w", err)
	}

	result := make(map[string]time.Time)

	// Scan disk for sessions
	sessionsDir := filepath.Join(m.root, agentID, "sessions")
	if entries, err := os.ReadDir(sessionsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				result[e.Name()] = time.Time{}
			}
		}
	}

	// Merge in-memory access times (override disk entries)
	prefix := agentID + "/"
	m.mu.RLock()
	for key, lastAccess := range m.accessTime {
		if strings.HasPrefix(key, prefix) {
			sessionID := strings.TrimPrefix(key, prefix)
			result[sessionID] = lastAccess
		}
	}
	m.mu.RUnlock()

	var sessions []SessionEntry
	for sid, lastAccess := range result {
		sessions = append(sessions, SessionEntry{
			SessionID:  sid,
			LastAccess: lastAccess,
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].LastAccess.After(sessions[j].LastAccess)
	})
	return sessions, nil
}

// DeleteSession removes a session directory, its audit file, and in-memory state.
func (m *Manager) DeleteSession(agentID, sessionID string) error {
	if err := audit.ValidateID(agentID); err != nil {
		return fmt.Errorf("invalid agent_id: %w", err)
	}
	if err := audit.ValidateID(sessionID); err != nil {
		return fmt.Errorf("invalid session_id: %w", err)
	}

	key := m.agentKey(agentID, sessionID)

	// Acquire the manager lock first, then close audit writer.
	// This ensures consistent ordering: m.mu is always acquired before auditWriter operations.
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.auditWriter != nil {
		m.auditWriter.CloseSession(agentID, sessionID)
	}

	// Remove session directory
	os.RemoveAll(m.SessionRoot(agentID, sessionID))

	// Remove audit file
	os.Remove(m.AuditPath(agentID, sessionID))

	// Remove from access time map
	delete(m.accessTime, key)
	return nil
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
				agentID, sessionID := parts[0], parts[1]
				// Close audit writer handle (lock ordering: m.mu held first)
				if m.auditWriter != nil {
					m.auditWriter.CloseSession(agentID, sessionID)
				}
				// Remove session directory
				os.RemoveAll(m.SessionRoot(agentID, sessionID))
				// Remove audit file
				os.Remove(m.AuditPath(agentID, sessionID))
			}
			delete(m.accessTime, key)
		}
	}
}
