package handler

import (
	"archive/zip"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
)

const (
	maxZipSize        = 100 * 1024 * 1024 // 100MB download limit
	maxExtractedSize  = 500 * 1024 * 1024 // 500MB total extracted size
	skillHTTPTimeout  = 60 * time.Second
	sourceFile        = ".source"
	ssrfProtectionEnv = "SANDBOX_SSRF_PROTECTION"
)

var defaultHTTPClient = &http.Client{
	Timeout: skillHTTPTimeout,
}

// SkillHandler handles skill list/load API endpoints.
type SkillHandler struct {
	mgr            *session.Manager
	mu             sync.Mutex
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

// skillFrontmatter represents the YAML frontmatter in a SKILLS.MD file.
type skillFrontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Type        string `yaml:"type"`
}

// extractSlugFromURL extracts a skill name from a URL.
// Priority: slug query param > last path segment (without extension) > "skill-N".
func extractSlugFromURL(rawURL string, index int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Sprintf("skill-%d", index)
	}

	if slug := u.Query().Get("slug"); slug != "" {
		return sanitizeName(slug)
	}

	base := filepath.Base(u.Path)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base != "" && base != "." && base != "/" {
		return sanitizeName(base)
	}

	return fmt.Sprintf("skill-%d", index)
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, " ", "-")
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if result == "" {
		return "unnamed-skill"
	}
	return result
}

// validateSkillURL checks if a URL is safe to fetch (SSRF protection).
// Blocks private, loopback, link-local, and unspecified IPs.
// Skips all checks when SSRF protection is disabled.
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

// parseSkillMeta reads skill metadata from the SKILLS.MD or SKILL.md file.
func parseSkillMeta(skillDir, slug string) model.SkillMeta {
	meta := model.SkillMeta{
		Name: slug,
		Path: "/skills/" + slug,
	}
	skillsMD := filepath.Join(skillDir, "SKILLS.MD")
	if _, err := os.Stat(skillsMD); err != nil {
		skillsMD = filepath.Join(skillDir, "SKILL.md")
	}
	if content, err := os.ReadFile(skillsMD); err == nil {
		parseFrontmatter(content, &meta)
	}
	return meta
}

// List downloads skill ZIPs, extracts them, parses metadata, and returns the list.
// Skills already cached (same URL) are skipped. Skills not in the current request are removed.
func (h *SkillHandler) List(c *gin.Context) {
	var req model.SkillListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	skillsDir := h.mgr.SkillsRoot(req.AgentID)
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create skills directory: "+err.Error()))
		return
	}

	// Serialize to prevent TOCTOU races on skill directories
	h.mu.Lock()
	defer h.mu.Unlock()

	activeSlugs := make(map[string]bool)
	var skills []model.SkillMeta

	for i, rawURL := range req.SkillURLs {
		slug := extractSlugFromURL(rawURL, i)
		activeSlugs[slug] = true
		skillDir := filepath.Join(skillsDir, slug)

		// Check cache: skip download if skill exists with same source URL
		if cachedSource, err := os.ReadFile(filepath.Join(skillDir, sourceFile)); err == nil && string(cachedSource) == rawURL {
			skills = append(skills, parseSkillMeta(skillDir, slug))
			continue
		}

		// SSRF protection
		if err := h.validateSkillURL(rawURL); err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse("blocked URL for skill "+slug+": "+err.Error()))
			return
		}

		// Download ZIP with timeout
		resp, err := h.client().Get(rawURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill "+slug+": "+err.Error()))
			return
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill "+slug+": HTTP "+resp.Status))
			return
		}

		// Save to temp file with size limit
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
			c.JSON(http.StatusBadRequest, model.ErrResponse("skill zip exceeds size limit (100MB): "+slug))
			return
		}

		// Remove existing skill directory and extract
		os.RemoveAll(skillDir)
		if err := extractZip(tmpPath, skillDir); err != nil {
			os.Remove(tmpPath)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to extract skill "+slug+": "+err.Error()))
			return
		}
		os.Remove(tmpPath)

		// Write source file for cache validation
		if err := os.WriteFile(filepath.Join(skillDir, sourceFile), []byte(rawURL), 0644); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write cache metadata: "+err.Error()))
			return
		}

		skills = append(skills, parseSkillMeta(skillDir, slug))
	}

	// Cleanup: remove skill directories not in current request
	cleanupStaleSkills(skillsDir, activeSlugs)

	if skills == nil {
		skills = []model.SkillMeta{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillListResult{
		Skills: skills,
	}))
}

// cleanupStaleSkills removes skill directories that are not in the active set.
func cleanupStaleSkills(skillsDir string, activeSlugs map[string]bool) {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !activeSlugs[entry.Name()] {
			os.RemoveAll(filepath.Join(skillsDir, entry.Name()))
		}
	}
}

// Load returns the SKILLS.MD content for the requested skill names.
func (h *SkillHandler) Load(c *gin.Context) {
	var req model.SkillLoadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	skillsDir := h.mgr.SkillsRoot(req.AgentID)
	var skills []model.SkillContent

	for _, name := range req.SkillNames {
		skillDir := filepath.Join(skillsDir, name)

		skillsMD := filepath.Join(skillDir, "SKILLS.MD")
		if _, err := os.Stat(skillsMD); err != nil {
			skillsMD = filepath.Join(skillDir, "SKILL.md")
		}

		content, err := os.ReadFile(skillsMD)
		if err != nil {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+name))
			return
		}
		skills = append(skills, model.SkillContent{
			Name:    name,
			Content: string(content),
		})
	}

	if skills == nil {
		skills = []model.SkillContent{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillLoadResult{
		Skills: skills,
	}))
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

	for _, f := range r.File {
		// Security: skip paths with ".."
		if strings.Contains(f.Name, "..") {
			continue
		}

		fpath := filepath.Join(destDir, f.Name)

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, 0755)
			continue
		}

		// Track total extracted size to mitigate zip bombs
		totalSize += int64(f.UncompressedSize64)
		if totalSize > maxExtractedSize {
			return fmt.Errorf("total extracted size exceeds limit (%dMB)", maxExtractedSize/1024/1024)
		}

		// Ensure parent directory
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

// parseFrontmatter parses YAML frontmatter from SKILLS.MD content.
func parseFrontmatter(content []byte, meta *model.SkillMeta) {
	text := string(content)

	if !strings.HasPrefix(text, "---") {
		return
	}

	end := strings.Index(text[3:], "---")
	if end < 0 {
		return
	}

	fmText := strings.TrimSpace(text[3 : end+3])

	var fm skillFrontmatter
	if err := yaml.Unmarshal([]byte(fmText), &fm); err != nil {
		return
	}

	if fm.Name != "" {
		meta.Name = fm.Name
	}
	if fm.Description != "" {
		meta.Description = fm.Description
	}
	if fm.Type != "" {
		meta.Type = fm.Type
	}
}
