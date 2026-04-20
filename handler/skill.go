package handler

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

const (
	maxZipSize        = 100 * 1024 * 1024 // 100MB download limit
	maxExtractedSize  = 500 * 1024 * 1024 // 500MB total extracted size
	maxUploadFiles    = 20                 // max files per upload request
	skillHTTPTimeout  = 60 * time.Second
	metaFile          = "_meta.json"
	ssrfProtectionEnv = "SANDBOX_SSRF_PROTECTION"
)

var validSkillID = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

var defaultHTTPClient = &http.Client{
	Timeout: skillHTTPTimeout,
}

// SkillHandler handles agent skill API endpoints.
type SkillHandler struct {
	mgr            *session.Manager
	mu             sync.RWMutex
	httpClient     *http.Client
	ssrfProtection bool
}

// NewSkillHandler creates a new SkillHandler with SSRF protection from env.
func NewSkillHandler(mgr *session.Manager) *SkillHandler {
	return &SkillHandler{
		mgr:            mgr,
		ssrfProtection: isSSRFProtectionEnabled(),
	}
}

// SetSSRFProtection enables or disables SSRF protection.
func (h *SkillHandler) SetSSRFProtection(enabled bool) {
	h.ssrfProtection = enabled
}

func (h *SkillHandler) client() *http.Client {
	if h.httpClient != nil {
		return h.httpClient
	}
	return defaultHTTPClient
}

func isSSRFProtectionEnabled() bool {
	v := strings.ToLower(os.Getenv(ssrfProtectionEnv))
	return v != "false" && v != "0" && v != "off"
}

// ——— Helpers ———

func validateSkillID(id string) error {
	if id == "" {
		return fmt.Errorf("skill ID is required")
	}
	if len(id) > 128 {
		return fmt.Errorf("skill ID too long (max 128 chars)")
	}
	if !validSkillID.MatchString(id) {
		return fmt.Errorf("skill ID contains invalid characters (only letters, digits, and hyphens allowed)")
	}
	return nil
}

func readSkillMeta(skillDir string) (*model.SkillMetaJSON, error) {
	data, err := os.ReadFile(filepath.Join(skillDir, metaFile))
	if err != nil {
		return nil, err
	}
	var meta model.SkillMetaJSON
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func writeSkillMeta(skillDir string, meta *model.SkillMetaJSON) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(skillDir, metaFile), data, 0644)
}

func resolveSkillFilePath(skillDir, relPath string) (string, error) {
	for _, component := range strings.Split(filepath.ToSlash(relPath), "/") {
		if component == ".." {
			return "", fmt.Errorf("path traversal not allowed: %s", relPath)
		}
	}
	clean := filepath.Clean(relPath)
	full := filepath.Join(skillDir, clean)
	full = filepath.Clean(full)
	if !strings.HasPrefix(full+string(os.PathSeparator), skillDir+string(os.PathSeparator)) && full != skillDir {
		return "", fmt.Errorf("path escapes skill directory: %s", relPath)
	}
	return full, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		// Use Lstat to detect symlinks that Walk may have resolved
		linfo, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		if linfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// extractZip extracts a ZIP archive to the target directory with size limits.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("create destination directory %s: %w", destDir, err)
	}

	var totalSize int64

	cleanDestDir := filepath.Clean(destDir)

	for _, f := range r.File {
		if strings.Contains(f.Name, "..") {
			continue
		}

		fpath := filepath.Join(destDir, f.Name)
		if !strings.HasPrefix(filepath.Clean(fpath)+string(os.PathSeparator), cleanDestDir+string(os.PathSeparator)) &&
			filepath.Clean(fpath) != cleanDestDir {
			continue
		}

		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			if err := os.MkdirAll(fpath, 0755); err != nil {
				return fmt.Errorf("create directory %s: %w", fpath, err)
			}
			continue
		}

		totalSize += int64(f.UncompressedSize64)
		if totalSize > maxExtractedSize {
			return fmt.Errorf("total extracted size exceeds limit (%dMB)", maxExtractedSize/1024/1024)
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return fmt.Errorf("create parent directory %s: %w", filepath.Dir(fpath), err)
		}

		outFile, err := os.OpenFile(fpath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()&0755)
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		rc.Close()
		outFile.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

// splitFrontmatter splits SKILLS.md content into frontmatter (YAML between --- delimiters)
// and body (everything after the second ---).
func splitFrontmatter(content string) (frontmatter, body string) {
	if !strings.HasPrefix(content, "---") {
		return "", content
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return "", content
	}
	frontmatter = strings.TrimSpace(content[3 : end+3])
	body = strings.TrimLeft(content[end+6:], "\n")
	return
}

// readSkillsMD reads the SKILLS.md (case-insensitive) from a skill directory.
// Returns the file path, content, and any error.
func readSkillsMD(skillDir, skillID string) (string, error) {
	for _, name := range []string{"SKILLS.md", "SKILLS.MD", "SKILL.md"} {
		p := filepath.Join(skillDir, name)
		if content, err := os.ReadFile(p); err == nil {
			return string(content), nil
		}
	}
	return "", fmt.Errorf("SKILLS.md not found for skill: %s", skillID)
}

// findSkillsMDFile reads the SKILLS.md and returns both its path and content.
func findSkillsMDFile(skillDir string) (path string, content string, err error) {
	for _, name := range []string{"SKILLS.md", "SKILLS.MD", "SKILL.md"} {
		p := filepath.Join(skillDir, name)
		if data, readErr := os.ReadFile(p); readErr == nil {
			return p, string(data), nil
		}
	}
	return "", "", fmt.Errorf("SKILLS.md not found in %s", skillDir)
}

// quoteYAMLDescription returns a JSON-quoted string safe for embedding in YAML frontmatter.
func quoteYAMLDescription(desc string) string {
	b, _ := json.Marshal(desc)
	return string(b)
}

// buildSkillsMDContent constructs a SKILLS.md file with frontmatter and body.
func buildSkillsMDContent(name, description, body string) string {
	return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s", name, quoteYAMLDescription(description), body)
}

// extractDescriptionFromFrontmatter extracts the description field from YAML frontmatter.
func extractDescriptionFromFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return ""
	}
	end := strings.Index(content[3:], "---")
	if end < 0 {
		return ""
	}
	fmText := strings.TrimSpace(content[3 : end+3])
	for _, line := range strings.Split(fmText, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "description:") {
			desc := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			// Remove surrounding quotes if present
			if len(desc) >= 2 && (desc[0] == '"' || desc[0] == '\'') && desc[0] == desc[len(desc)-1] {
				desc = desc[1 : len(desc)-1]
			}
			return desc
		}
	}
	return ""
}

// ——— Agent Skill Loading ———

// Sentinel error types for syncSkillToAgent, enabling reliable HTTP status mapping.
type errSkillNotFound struct{ msg string }

func (e *errSkillNotFound) Error() string { return e.msg }

type errSkillInvalid struct{ msg string }

func (e *errSkillInvalid) Error() string { return e.msg }

// copySkillToAgent copies a skill from global store to agent cache with proper locking.
func (h *SkillHandler) copySkillToAgent(globalDir, agentDir string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	os.RemoveAll(agentDir)
	if err := os.MkdirAll(filepath.Dir(agentDir), 0755); err != nil {
		log.Printf("[ERROR] copySkillToAgent %s: mkdir: %v", agentDir, err)
		return err
	}
	return copyDir(globalDir, agentDir)
}

// syncSkillToAgent validates a skill ID, checks the global store, and syncs to agent cache
// if needed. Returns the agent-local skill directory path.
// When agentWorkspace is true, skips version checking and uses the local copy as-is.
func (h *SkillHandler) syncSkillToAgent(agentID, skillID string, agentWorkspace bool) (string, error) {
	if err := validateSkillID(skillID); err != nil {
		return "", &errSkillInvalid{msg: fmt.Sprintf("invalid skill ID %q: %s", skillID, err.Error())}
	}

	agentDir := filepath.Join(h.mgr.SkillsRoot(agentID), skillID)

	if agentWorkspace {
		if _, err := os.Stat(agentDir); err != nil {
			return "", &errSkillNotFound{msg: "skill not found locally: " + skillID}
		}
		return agentDir, nil
	}

	globalDir := h.mgr.GlobalSkillPath(skillID)
	if _, err := os.Stat(globalDir); err != nil {
		return "", &errSkillNotFound{msg: "skill not found: " + skillID}
	}

	globalMeta, err := readSkillMeta(globalDir)
	if err != nil {
		return "", fmt.Errorf("failed to read skill metadata: %s", skillID)
	}

	needCopy := true

	if localMeta, err := readSkillMeta(agentDir); err == nil {
		if localMeta.UpdatedAt >= globalMeta.UpdatedAt {
			needCopy = false
		}
	}

	if needCopy {
		if err := h.copySkillToAgent(globalDir, agentDir); err != nil {
			return "", fmt.Errorf("failed to copy skill: %s", skillID)
		}
	}

	return agentDir, nil
}

// ——— Agent Skill APIs ———

// cleanupAgentSkillCache removes cached skill directories that are not in the requested set.
func (h *SkillHandler) cleanupAgentSkillCache(agentID string, requestedIDs []string) {
	skillsRoot := h.mgr.SkillsRoot(agentID)
	entries, err := os.ReadDir(skillsRoot)
	if err != nil {
		return
	}

	wanted := make(map[string]bool, len(requestedIDs))
	for _, id := range requestedIDs {
		wanted[id] = true
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !wanted[entry.Name()] {
			os.RemoveAll(filepath.Join(skillsRoot, entry.Name()))
		}
	}
}

// AgentList syncs skills to agent cache and returns frontmatter summaries.
func (h *SkillHandler) AgentList(c *gin.Context) {
	agentID := c.Param("agent_id")

	var req model.AgentSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	skills := make([]model.SkillSummary, 0, len(req.SkillIDs))

	for _, skillID := range req.SkillIDs {
		agentDir, err := h.syncSkillToAgent(agentID, skillID, req.EnableAgentWorkspace)
		if err != nil {
			log.Printf("[WARN] agent %s: skip skill %s: sync failed: %v", agentID, skillID, err)
			continue
		}

		content, err := readSkillsMD(agentDir, skillID)
		if err != nil {
			log.Printf("[WARN] agent %s: skip skill %s: read SKILLS.md failed: %v", agentID, skillID, err)
			continue
		}

		fm, _ := splitFrontmatter(content)

		skills = append(skills, model.SkillSummary{
			Name:        skillID,
			Path:        "/skills/" + skillID,
			Frontmatter: fm,
		})
	}

	if req.Cleanup {
		h.cleanupAgentSkillCache(agentID, req.SkillIDs)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.AgentSkillListResult{Skills: skills}))
}

// AgentLoad syncs skills to agent cache and returns SKILLS.md body (post-frontmatter).
func (h *SkillHandler) AgentLoad(c *gin.Context) {
	agentID := c.Param("agent_id")

	var req model.AgentSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	skills := make([]model.SkillContent, 0, len(req.SkillIDs))

	for _, skillID := range req.SkillIDs {
		agentDir, err := h.syncSkillToAgent(agentID, skillID, req.EnableAgentWorkspace)
		if err != nil {
			log.Printf("[WARN] agent %s: skip skill %s: sync failed: %v", agentID, skillID, err)
			continue
		}

		content, err := readSkillsMD(agentDir, skillID)
		if err != nil {
			log.Printf("[WARN] agent %s: skip skill %s: read SKILLS.md failed: %v", agentID, skillID, err)
			continue
		}

		_, body := splitFrontmatter(content)

		skills = append(skills, model.SkillContent{
			Name:    skillID,
			Content: body,
		})
	}

	if req.Cleanup {
		h.cleanupAgentSkillCache(agentID, req.SkillIDs)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.AgentSkillLoadResult{Skills: skills}))
}

// AgentCacheDelete deletes cached skills for an agent.
func (h *SkillHandler) AgentCacheDelete(c *gin.Context) {
	agentID := c.Param("agent_id")
	if err := validateSkillID(agentID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid agent_id: "+err.Error()))
		return
	}
	skillID := c.Query("skill_id")

	var deleted []string

	if skillID != "" {
		if err := validateSkillID(skillID); err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
			return
		}
		cacheDir := filepath.Join(h.mgr.SkillsRoot(agentID), skillID)
		if _, err := os.Stat(cacheDir); err != nil {
			c.JSON(http.StatusNotFound, model.ErrResponse("cached skill not found: "+skillID))
			return
		}
		if err := os.RemoveAll(cacheDir); err != nil {
			log.Printf("[ERROR] AgentCacheDelete: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to delete cache: "+err.Error()))
			return
		}
		deleted = append(deleted, skillID)
	} else {
		skillsRoot := h.mgr.SkillsRoot(agentID)
		entries, err := os.ReadDir(skillsRoot)
		if err != nil {
			c.JSON(http.StatusOK, model.OkResponse(model.AgentSkillCacheDeleteResult{Deleted: []string{}}))
			return
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if err := os.RemoveAll(filepath.Join(skillsRoot, entry.Name())); err != nil {
				log.Printf("[ERROR] AgentCacheDelete: remove %s: %v", entry.Name(), err)
				continue
			}
			deleted = append(deleted, entry.Name())
		}
	}

	if deleted == nil {
		deleted = []string{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.AgentSkillCacheDeleteResult{Deleted: deleted}))
}
