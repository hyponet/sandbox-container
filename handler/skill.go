package handler

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
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
	if content, err := readSkillsMD(skillDir, req.Name); err != nil {
		// Create default SKILLS.md if not in ZIP
		skillsMDPath := filepath.Join(skillDir, "SKILLS.md")
		defaultMD := fmt.Sprintf("---\nname: %s\n---\n", req.Name)
		if wErr := os.WriteFile(skillsMDPath, []byte(defaultMD), 0644); wErr != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write default SKILLS.md: "+wErr.Error()))
			return
		}
	} else {
		// Try to extract description from frontmatter
		desc := extractDescriptionFromFrontmatter(content)
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

// FileDelete deletes a file or directory within a global skill.
func (h *SkillHandler) FileDelete(c *gin.Context) {
	var req model.SkillFileDeleteRequest
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

	// Don't allow deleting _meta.json
	if strings.EqualFold(filepath.Base(resolved), metaFile) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot delete system file: "+metaFile))
		return
	}

	// Don't allow deleting the skill root itself
	if resolved == filepath.Clean(skillDir) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot delete skill root directory, use /v1/skills/delete instead"))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	// Use Lstat inside lock to avoid TOCTOU race and to not follow symlinks
	if _, err := os.Lstat(resolved); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("path not found: "+req.Path))
		return
	}

	if err := os.RemoveAll(resolved); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to delete: "+err.Error()))
		return
	}

	touchSkillMeta(skillDir)

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileDeleteResult{
		Path: req.Path,
	}))
}

// ImportUpload imports skills from uploaded ZIP files (multipart form).
// Accepts multiple files via the "files" form field. Each file must have a
// corresponding "names" form field value specifying the skill ID.
//
// NOTE: This operation is NOT atomic. If processing fails mid-way (e.g., on
// file 2 of 3), previously imported skills remain on disk. Callers should
// treat partial success as possible and verify results.
func (h *SkillHandler) ImportUpload(c *gin.Context) {
	// Limit total request body to prevent memory exhaustion.
	// Each file is also checked against maxZipSize individually.
	maxUploadBody := int64(len(c.Request.Header.Get("Content-Type"))+1024) + maxZipSize*10
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBody)

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid multipart form: "+err.Error()))
		return
	}

	files := form.File["files"]
	names := form.Value["names"]

	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, model.ErrResponse("no files uploaded"))
		return
	}
	if len(names) != len(files) {
		c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("names count (%d) must match files count (%d)", len(names), len(files))))
		return
	}

	// Validate all skill IDs upfront and reject duplicates
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if err := validateSkillID(name); err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("invalid skill name %q: %s", name, err.Error())))
			return
		}
		if seen[name] {
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("duplicate skill name: %q", name)))
			return
		}
		seen[name] = true
	}

	results := make([]model.SkillMetaJSON, 0, len(files))

	for i, fh := range files {
		skillName := names[i]

		if fh.Size > maxZipSize {
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("file %q exceeds size limit (100MB)", fh.Filename)))
			return
		}

		// Save uploaded file to temp
		src, err := fh.Open()
		if err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse(fmt.Sprintf("failed to open uploaded file %q: %s", fh.Filename, err.Error())))
			return
		}

		tmpFile, err := os.CreateTemp("", "skill-upload-*.zip")
		if err != nil {
			src.Close()
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create temp file: "+err.Error()))
			return
		}
		tmpPath := tmpFile.Name()

		_, err = io.Copy(tmpFile, src)
		src.Close()
		tmpFile.Close()
		if err != nil {
			os.Remove(tmpPath)
			c.JSON(http.StatusInternalServerError, model.ErrResponse(fmt.Sprintf("failed to save uploaded file %q: %s", fh.Filename, err.Error())))
			return
		}

		meta, err := h.importSkillFromZip(skillName, tmpPath)
		os.Remove(tmpPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse(fmt.Sprintf("failed to import %q: %s", fh.Filename, err.Error())))
			return
		}

		results = append(results, *meta)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillImportUploadResult{Skills: results}))
}

// importSkillFromZip extracts a ZIP into a skill directory with per-skill locking.
func (h *SkillHandler) importSkillFromZip(skillName, zipPath string) (*model.SkillMetaJSON, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.GlobalSkillPath(skillName)

	// Preserve created_at if skill already exists
	var existingCreatedAt int64
	if existingMeta, err := readSkillMeta(skillDir); err == nil {
		existingCreatedAt = existingMeta.CreatedAt
	}

	// Remove existing and extract
	os.RemoveAll(skillDir)
	if err := extractZip(zipPath, skillDir); err != nil {
		return nil, fmt.Errorf("extract: %w", err)
	}

	// Write/update _meta.json
	now := time.Now().UnixNano()
	if existingCreatedAt == 0 {
		existingCreatedAt = now
	}
	meta := &model.SkillMetaJSON{
		Name:      skillName,
		CreatedAt: existingCreatedAt,
		UpdatedAt: now,
	}

	if content, err := readSkillsMD(skillDir, skillName); err != nil {
		defaultMD := fmt.Sprintf("---\nname: %s\n---\n", skillName)
		if wErr := os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte(defaultMD), 0644); wErr != nil {
			return nil, fmt.Errorf("write default SKILLS.md: %w", wErr)
		}
	} else {
		if desc := extractDescriptionFromFrontmatter(content); desc != "" {
			meta.Description = desc
		}
	}

	if err := writeSkillMeta(skillDir, meta); err != nil {
		return nil, fmt.Errorf("write metadata: %w", err)
	}

	return meta, nil
}

// ——— Agent Skill Loading ———

// Sentinel error types for syncSkillToAgent, enabling reliable HTTP status mapping.
type errSkillNotFound struct{ msg string }

func (e *errSkillNotFound) Error() string { return e.msg }

type errSkillInvalid struct{ msg string }

func (e *errSkillInvalid) Error() string { return e.msg }

// syncErrToStatus maps syncSkillToAgent errors to HTTP status codes.
func syncErrToStatus(err error) int {
	switch err.(type) {
	case *errSkillNotFound:
		return http.StatusNotFound
	case *errSkillInvalid:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
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

// copySkillToAgent copies a skill from global store to agent cache with proper locking.
func (h *SkillHandler) copySkillToAgent(globalDir, agentDir string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	os.RemoveAll(agentDir)
	os.MkdirAll(filepath.Dir(agentDir), 0755)
	return copyDir(globalDir, agentDir)
}

// syncSkillToAgent validates a skill ID, checks the global store, and syncs to agent cache
// if needed. Returns the agent-local skill directory path.
func (h *SkillHandler) syncSkillToAgent(agentID, skillID string) (string, error) {
	if err := validateSkillID(skillID); err != nil {
		return "", &errSkillInvalid{msg: fmt.Sprintf("invalid skill ID %q: %s", skillID, err.Error())}
	}

	globalDir := h.mgr.GlobalSkillPath(skillID)
	if _, err := os.Stat(globalDir); err != nil {
		return "", &errSkillNotFound{msg: "skill not found: " + skillID}
	}

	globalMeta, err := readSkillMeta(globalDir)
	if err != nil {
		return "", fmt.Errorf("failed to read skill metadata: %s", skillID)
	}

	agentDir := filepath.Join(h.mgr.SkillsRoot(agentID), skillID)
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

// readSkillsMD reads the SKILLS.md (case-insensitive) from a skill directory.
func readSkillsMD(skillDir, skillID string) (string, error) {
	for _, name := range []string{"SKILLS.md", "SKILLS.MD", "SKILL.md"} {
		p := filepath.Join(skillDir, name)
		if content, err := os.ReadFile(p); err == nil {
			return string(content), nil
		}
	}
	return "", fmt.Errorf("SKILLS.md not found for skill: %s", skillID)
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
		agentDir, err := h.syncSkillToAgent(agentID, skillID)
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
		agentDir, err := h.syncSkillToAgent(agentID, skillID)
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

	c.JSON(http.StatusOK, model.OkResponse(model.AgentSkillLoadResult{Skills: skills}))
}
