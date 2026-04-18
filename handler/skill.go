package handler

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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
	skillHTTPTimeout  = 60 * time.Second
	metaFile          = "_meta.json"
	ssrfProtectionEnv = "SANDBOX_SSRF_PROTECTION"
)

var validSkillID = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

var defaultHTTPClient = &http.Client{
	Timeout: skillHTTPTimeout,
}

// SkillHandler handles skill management API endpoints.
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

func touchSkillMeta(skillDir string) error {
	meta, err := readSkillMeta(skillDir)
	if err != nil {
		return err
	}
	meta.UpdatedAt = time.Now().UnixNano()
	return writeSkillMeta(skillDir, meta)
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
		// Skip symlinks to prevent symlink escape attacks
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
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

// validateSkillURL checks if a URL is safe to fetch (SSRF protection).
func (h *SkillHandler) validateSkillURL(rawURL string) error {
	if !h.ssrfProtection {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q (only http/https allowed)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host in URL")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve host %s: %w", host, err)
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL resolves to private/reserved IP %s", ip)
		}
	}

	return nil
}

// extractZip extracts a ZIP archive to the target directory with size limits.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	os.MkdirAll(destDir, 0755)

	var totalSize int64

	cleanDestDir := filepath.Clean(destDir)

	for _, f := range r.File {
		if strings.Contains(f.Name, "..") {
			continue
		}

		fpath := filepath.Join(destDir, f.Name)
		// Validate extracted path stays within destDir
		if !strings.HasPrefix(filepath.Clean(fpath)+string(os.PathSeparator), cleanDestDir+string(os.PathSeparator)) &&
			filepath.Clean(fpath) != cleanDestDir {
			continue
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, 0755)
			continue
		}

		totalSize += int64(f.UncompressedSize64)
		if totalSize > maxExtractedSize {
			return fmt.Errorf("total extracted size exceeds limit (%dMB)", maxExtractedSize/1024/1024)
		}

		os.MkdirAll(filepath.Dir(fpath), 0755)

		outFile, err := os.OpenFile(fpath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
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

// ——— Global Skill Management APIs ———

// Create creates a new empty skill in the global store.
func (h *SkillHandler) Create(c *gin.Context) {
	var req model.SkillCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.GlobalSkillPath(req.Name)

	// Check if already exists
	if _, err := os.Stat(skillDir); err == nil {
		c.JSON(http.StatusConflict, model.ErrResponse("skill already exists: "+req.Name))
		return
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create skill directory: "+err.Error()))
		return
	}

	now := time.Now().UnixNano()
	meta := &model.SkillMetaJSON{
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := writeSkillMeta(skillDir, meta); err != nil {
		os.RemoveAll(skillDir)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	// Create default SKILLS.md (quote description to prevent YAML injection)
	safeDesc := strings.ReplaceAll(req.Description, `"`, `\"`)
	skillsMD := fmt.Sprintf("---\nname: %s\ndescription: \"%s\"\n---\n", req.Name, safeDesc)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte(skillsMD), 0644); err != nil {
		os.RemoveAll(skillDir)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write SKILLS.md: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillCreateResult{Skill: *meta}))
}

// Import imports a skill from a ZIP URL into the global store.
func (h *SkillHandler) Import(c *gin.Context) {
	var req model.SkillImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	// SSRF validation
	if err := h.validateSkillURL(req.ZipURL); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("blocked URL: "+err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.GlobalSkillPath(req.Name)

	// Preserve created_at if skill already exists
	var existingCreatedAt int64
	if existingMeta, err := readSkillMeta(skillDir); err == nil {
		existingCreatedAt = existingMeta.CreatedAt
	}

	// Download ZIP
	resp, err := h.client().Get(req.ZipURL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill: "+err.Error()))
		return
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill: HTTP "+resp.Status))
		return
	}

	tmpFile, err := os.CreateTemp("", "skill-*.zip")
	if err != nil {
		resp.Body.Close()
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create temp file: "+err.Error()))
		return
	}
	tmpPath := tmpFile.Name()

	written, err := io.Copy(tmpFile, io.LimitReader(resp.Body, maxZipSize+1))
	resp.Body.Close()
	tmpFile.Close()

	if err != nil {
		os.Remove(tmpPath)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to save skill zip: "+err.Error()))
		return
	}
	if written > maxZipSize {
		os.Remove(tmpPath)
		c.JSON(http.StatusBadRequest, model.ErrResponse("skill zip exceeds size limit (100MB)"))
		return
	}

	// Remove existing and extract
	os.RemoveAll(skillDir)
	if err := extractZip(tmpPath, skillDir); err != nil {
		os.Remove(tmpPath)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to extract skill: "+err.Error()))
		return
	}
	os.Remove(tmpPath)

	// Write/update _meta.json
	now := time.Now().UnixNano()
	if existingCreatedAt == 0 {
		existingCreatedAt = now
	}
	meta := &model.SkillMetaJSON{
		Name:        req.Name,
		Description: "",
		CreatedAt:   existingCreatedAt,
		UpdatedAt:   now,
	}

	// Try to extract description from SKILLS.md frontmatter if present
	skillsMDPath := filepath.Join(skillDir, "SKILLS.md")
	if content, err := os.ReadFile(skillsMDPath); err != nil {
		// Create default SKILLS.md if not in ZIP
		defaultMD := fmt.Sprintf("---\nname: %s\n---\n", req.Name)
		if wErr := os.WriteFile(skillsMDPath, []byte(defaultMD), 0644); wErr != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write default SKILLS.md: "+wErr.Error()))
			return
		}
	} else {
		// Try to extract description from frontmatter
		desc := extractDescriptionFromFrontmatter(string(content))
		if desc != "" {
			meta.Description = desc
		}
	}

	if err := writeSkillMeta(skillDir, meta); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillImportResult{Skill: *meta}))
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

// ListGlobal lists all skills in the global store.
func (h *SkillHandler) ListGlobal(c *gin.Context) {
	globalDir := h.mgr.GlobalSkillsRoot()
	entries, err := os.ReadDir(globalDir)
	if err != nil {
		c.JSON(http.StatusOK, model.OkResponse(model.SkillGlobalListResult{Skills: []model.SkillMetaJSON{}}))
		return
	}

	var skills []model.SkillMetaJSON
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := readSkillMeta(filepath.Join(globalDir, entry.Name()))
		if err != nil {
			continue
		}
		skills = append(skills, *meta)
	}

	if skills == nil {
		skills = []model.SkillMetaJSON{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillGlobalListResult{Skills: skills}))
}

// Delete removes a global skill.
func (h *SkillHandler) Delete(c *gin.Context) {
	var req model.SkillDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.GlobalSkillPath(req.Name)
	if _, err := os.Stat(skillDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	if err := os.RemoveAll(skillDir); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to delete skill: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkMsg("skill deleted: "+req.Name))
}

// Tree returns the directory tree of a global skill.
func (h *SkillHandler) Tree(c *gin.Context) {
	var req model.SkillTreeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	skillDir := h.mgr.GlobalSkillPath(req.Name)
	if _, err := os.Stat(skillDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	var files []model.SkillFileEntry
	filepath.Walk(skillDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(skillDir, path)
		if rel == "." {
			return nil
		}
		files = append(files, model.SkillFileEntry{
			Path:        rel,
			IsDirectory: info.IsDir(),
			Size:        info.Size(),
		})
		return nil
	})

	if files == nil {
		files = []model.SkillFileEntry{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillTreeResult{
		Name:  req.Name,
		Files: files,
	}))
}

// FileRead reads a file within a global skill.
func (h *SkillHandler) FileRead(c *gin.Context) {
	var req model.SkillFileReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	skillDir := h.mgr.GlobalSkillPath(req.Name)
	if _, err := os.Stat(skillDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	resolved, err := resolveSkillFilePath(skillDir, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found: "+req.Path))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileReadResult{
		Path:    req.Path,
		Content: string(content),
	}))
}

// FileWrite writes a file within a global skill.
func (h *SkillHandler) FileWrite(c *gin.Context) {
	var req model.SkillFileWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	skillDir := h.mgr.GlobalSkillPath(req.Name)
	if _, err := os.Stat(skillDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	resolved, err := resolveSkillFilePath(skillDir, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	// Don't allow overwriting _meta.json directly (case-insensitive for macOS)
	if strings.EqualFold(filepath.Base(resolved), metaFile) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot write to system file: "+metaFile))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Ensure parent directory inside lock to prevent TOCTOU
	os.MkdirAll(filepath.Dir(resolved), 0755)

	data := []byte(req.Content)
	if err := os.WriteFile(resolved, data, 0644); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
		return
	}

	touchSkillMeta(skillDir)

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileWriteResult{
		Path:         req.Path,
		BytesWritten: len(data),
	}))
}

// FileUpdate replaces string content in a file within a global skill.
func (h *SkillHandler) FileUpdate(c *gin.Context) {
	var req model.SkillFileUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	skillDir := h.mgr.GlobalSkillPath(req.Name)
	if _, err := os.Stat(skillDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	resolved, err := resolveSkillFilePath(skillDir, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	if strings.EqualFold(filepath.Base(resolved), metaFile) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot update system file: "+metaFile))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	content, err := os.ReadFile(resolved)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found: "+req.Path))
		return
	}

	replacedCount := strings.Count(string(content), req.OldStr)

	// Skip write and meta touch when no replacements found
	if replacedCount > 0 {
		newContent := strings.ReplaceAll(string(content), req.OldStr, req.NewStr)
		if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
			return
		}
		touchSkillMeta(skillDir)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileUpdateResult{
		Path:          req.Path,
		ReplacedCount: replacedCount,
	}))
}

// FileMkdir creates a directory within a global skill.
func (h *SkillHandler) FileMkdir(c *gin.Context) {
	var req model.SkillFileMkdirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	skillDir := h.mgr.GlobalSkillPath(req.Name)
	if _, err := os.Stat(skillDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	resolved, err := resolveSkillFilePath(skillDir, req.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := os.MkdirAll(resolved, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create directory: "+err.Error()))
		return
	}

	touchSkillMeta(skillDir)

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileMkdirResult{
		Path: req.Path,
	}))
}

// ——— Agent Skill Loading ———

// copySkillToAgent copies a skill from global store to agent cache with proper locking.
func (h *SkillHandler) copySkillToAgent(globalDir, agentDir string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	os.RemoveAll(agentDir)
	os.MkdirAll(filepath.Dir(agentDir), 0755)
	return copyDir(globalDir, agentDir)
}

// Load loads skills into an agent's local cache and returns their content.
// For each skill_id: checks global store, compares version with agent cache,
// copies from global if outdated or missing, then returns SKILLS.md content.
func (h *SkillHandler) Load(c *gin.Context) {
	var req model.SkillLoadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	var skills []model.SkillContent

	for _, skillID := range req.SkillIDs {
		if err := validateSkillID(skillID); err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("invalid skill ID %q: %s", skillID, err.Error())))
			return
		}

		globalDir := h.mgr.GlobalSkillPath(skillID)
		if _, err := os.Stat(globalDir); err != nil {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+skillID))
			return
		}

		// Read global meta
		globalMeta, err := readSkillMeta(globalDir)
		if err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+skillID))
			return
		}

		agentDir := filepath.Join(h.mgr.SkillsRoot(req.AgentID), skillID)
		needCopy := true

		// Check agent cache
		if localMeta, err := readSkillMeta(agentDir); err == nil {
			if localMeta.UpdatedAt >= globalMeta.UpdatedAt {
				needCopy = false
			}
		}

		if needCopy {
			if err := h.copySkillToAgent(globalDir, agentDir); err != nil {
				c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to copy skill: "+skillID))
				return
			}
		}

		// Read SKILLS.md from agent-local copy
		skillsMD := filepath.Join(agentDir, "SKILLS.md")
		if _, err := os.Stat(skillsMD); err != nil {
			skillsMD = filepath.Join(agentDir, "SKILL.md")
		}

		content, err := os.ReadFile(skillsMD)
		if err != nil {
			c.JSON(http.StatusNotFound, model.ErrResponse("SKILLS.md not found for skill: "+skillID))
			return
		}

		skills = append(skills, model.SkillContent{
			Name:    skillID,
			Content: string(content),
		})
	}

	if skills == nil {
		skills = []model.SkillContent{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillLoadResult{Skills: skills}))
}
