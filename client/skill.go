package client

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

// SkillUploadEntry pairs a skill name with a local ZIP file path for upload.
type SkillUploadEntry struct {
	Name    string // Skill ID (letters, digits, hyphens)
	ZipPath string // Local path to the ZIP file
}

// SkillCreate creates a new empty skill in the global store.
func (c *Client) SkillCreate(name, description string) (*SkillCreateResult, error) {
	req := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{
		Name:        name,
		Description: description,
	}

	var result SkillCreateResult
	if err := c.post("/v1/skills/create", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillImport imports a skill from a ZIP URL into the global store.
func (c *Client) SkillImport(name, zipURL string) (*SkillImportResult, error) {
	req := struct {
		Name   string `json:"name"`
		ZipURL string `json:"zip_url"`
	}{
		Name:   name,
		ZipURL: zipURL,
	}

	var result SkillImportResult
	if err := c.post("/v1/skills/import", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillList lists all skills in the global store.
func (c *Client) SkillList() (*SkillGlobalListResult, error) {
	req := struct{}{}

	var result SkillGlobalListResult
	if err := c.post("/v1/skills/list", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillDelete deletes a global skill.
func (c *Client) SkillDelete(name string) error {
	req := struct {
		Name string `json:"name"`
	}{
		Name: name,
	}

	var result struct{}
	return c.post("/v1/skills/delete", req, &result)
}

// SkillTree returns the directory tree of a global skill.
func (c *Client) SkillTree(name string) (*SkillTreeResult, error) {
	req := struct {
		Name string `json:"name"`
	}{
		Name: name,
	}

	var result SkillTreeResult
	if err := c.post("/v1/skills/tree", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillFileRead reads a file within a global skill.
func (c *Client) SkillFileRead(name, path string) (*SkillFileReadResult, error) {
	req := struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}{
		Name: name,
		Path: path,
	}

	var result SkillFileReadResult
	if err := c.post("/v1/skills/file/read", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillFileWrite writes a file within a global skill.
func (c *Client) SkillFileWrite(name, path, content string) (*SkillFileWriteResult, error) {
	req := struct {
		Name    string `json:"name"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}{
		Name:    name,
		Path:    path,
		Content: content,
	}

	var result SkillFileWriteResult
	if err := c.post("/v1/skills/file/write", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillFileUpdate replaces string content in a file within a global skill.
func (c *Client) SkillFileUpdate(name, path, oldStr, newStr string) (*SkillFileUpdateResult, error) {
	req := struct {
		Name   string `json:"name"`
		Path   string `json:"path"`
		OldStr string `json:"old_str"`
		NewStr string `json:"new_str"`
	}{
		Name:   name,
		Path:   path,
		OldStr: oldStr,
		NewStr: newStr,
	}

	var result SkillFileUpdateResult
	if err := c.post("/v1/skills/file/update", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillFileMkdir creates a directory within a global skill.
func (c *Client) SkillFileMkdir(name, path string) (*SkillFileMkdirResult, error) {
	req := struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}{
		Name: name,
		Path: path,
	}

	var result SkillFileMkdirResult
	if err := c.post("/v1/skills/file/mkdir", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillFileDelete deletes a file or directory within a global skill.
func (c *Client) SkillFileDelete(name, path string) (*SkillFileDeleteResult, error) {
	req := struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}{
		Name: name,
		Path: path,
	}

	var result SkillFileDeleteResult
	if err := c.post("/v1/skills/file/delete", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillImportUpload imports skills from local ZIP files via multipart upload.
// Each entry pairs a skill name with a ZIP file path on disk.
// Note: all ZIP files are fully buffered in memory before sending. For large
// or numerous files, consider calling SkillImport (URL-based) instead.
func (c *Client) SkillImportUpload(entries []SkillUploadEntry) (*SkillImportUploadResult, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for _, e := range entries {
		if err := writer.WriteField("names", e.Name); err != nil {
			return nil, fmt.Errorf("write name field: %w", err)
		}
		part, err := writer.CreateFormFile("files", filepath.Base(e.ZipPath))
		if err != nil {
			return nil, fmt.Errorf("create form file: %w", err)
		}
		f, err := os.Open(e.ZipPath)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", e.ZipPath, err)
		}
		_, err = io.Copy(part, f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("copy %s: %w", e.ZipPath, err)
		}
	}
	writer.Close()

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/v1/skills/import/upload", body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	var result SkillImportUploadResult
	if err := c.handleResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillAgentList syncs skills to agent cache and returns frontmatter summaries.
func (c *Client) SkillAgentList(agentID string, skillIDs []string) (*AgentSkillListResult, error) {
	req := struct {
		SkillIDs []string `json:"skill_ids"`
	}{
		SkillIDs: skillIDs,
	}

	var result AgentSkillListResult
	if err := c.post("/v1/skills/agents/"+agentID+"/list", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillAgentLoad syncs skills to agent cache and returns SKILLS.md body content.
func (c *Client) SkillAgentLoad(agentID string, skillIDs []string) (*AgentSkillLoadResult, error) {
	req := struct {
		SkillIDs []string `json:"skill_ids"`
	}{
		SkillIDs: skillIDs,
	}

	var result AgentSkillLoadResult
	if err := c.post("/v1/skills/agents/"+agentID+"/load", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
