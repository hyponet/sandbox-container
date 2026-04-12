package handler

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
	"github.com/goccy/go-yaml"
)

type SkillHandler struct {
	mgr *session.Manager
}

func NewSkillHandler(mgr *session.Manager) *SkillHandler {
	return &SkillHandler{mgr: mgr}
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

	// Try slug query parameter
	if slug := u.Query().Get("slug"); slug != "" {
		return sanitizeName(slug)
	}

	// Use last path segment without extension
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
	// Keep only alphanumeric, dash, underscore, dot
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

// List downloads skill ZIPs, extracts them, parses metadata, and returns the list.
func (h *SkillHandler) List(c *gin.Context) {
	var req model.SkillListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	skillsDir := h.mgr.SkillsRoot(req.AgentID)
	os.MkdirAll(skillsDir, 0755)

	var skills []model.SkillMeta

	for i, rawURL := range req.SkillURLs {
		slug := extractSlugFromURL(rawURL, i)
		skillDir := filepath.Join(skillsDir, slug)

		// Download ZIP
		resp, err := http.Get(rawURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill "+slug+": "+err.Error()))
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill "+slug+": HTTP "+resp.Status))
			return
		}

		// Save to temp file
		tmpFile, err := os.CreateTemp("", "skill-*.zip")
		if err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create temp file: "+err.Error()))
			return
		}
		tmpPath := tmpFile.Name()
		defer os.Remove(tmpPath)

		if _, err := io.Copy(tmpFile, resp.Body); err != nil {
			tmpFile.Close()
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to save skill zip: "+err.Error()))
			return
		}
		tmpFile.Close()

		// Remove existing skill directory and extract
		os.RemoveAll(skillDir)
		if err := extractZip(tmpPath, skillDir); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to extract skill "+slug+": "+err.Error()))
			return
		}

		// Parse metadata from SKILLS.MD or SKILL.md
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

		skills = append(skills, meta)
	}

	if skills == nil {
		skills = []model.SkillMeta{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillListResult{
		Skills: skills,
	}))
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

		// Try SKILLS.MD first, then SKILL.md
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

// extractZip extracts a ZIP archive to the target directory.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	os.MkdirAll(destDir, 0755)

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

	// Check for frontmatter delimiters
	if !strings.HasPrefix(text, "---") {
		return
	}

	// Find closing ---
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
