package client

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// RegistrySkillCreate creates a new empty skill in the registry.
func (c *Client) RegistrySkillCreate(name, description string) (*RegistrySkillCreateResult, error) {
	req := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{
		Name:        name,
		Description: description,
	}

	var result RegistrySkillCreateResult
	if err := c.post("/v1/registry/create", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistrySkillGet retrieves a skill's metadata and active version's SKILLS.md content.
func (c *Client) RegistrySkillGet(name string) (*RegistrySkillGetResult, error) {
	req := struct {
		Name string `json:"name"`
	}{
		Name: name,
	}

	var result RegistrySkillGetResult
	if err := c.post("/v1/registry/get", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistrySkillUpdate updates a skill's description.
func (c *Client) RegistrySkillUpdate(name, description string) (*RegistrySkillUpdateResult, error) {
	req := struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}{
		Name:        name,
		Description: description,
	}

	var result RegistrySkillUpdateResult
	if err := c.post("/v1/registry/update", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistrySkillDelete deletes a skill from the registry and its deployment.
func (c *Client) RegistrySkillDelete(name string) error {
	req := struct {
		Name string `json:"name"`
	}{
		Name: name,
	}

	return c.post("/v1/registry/delete", req, nil)
}

// RegistrySkillList lists all skills in the registry.
func (c *Client) RegistrySkillList() (*RegistrySkillListResult, error) {
	var result RegistrySkillListResult
	if err := c.post("/v1/registry/list", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistrySkillRename renames a skill.
func (c *Client) RegistrySkillRename(name, newName string) (*RegistrySkillRenameResult, error) {
	req := struct {
		Name    string `json:"name"`
		NewName string `json:"new_name"`
	}{
		Name:    name,
		NewName: newName,
	}

	var result RegistrySkillRenameResult
	if err := c.post("/v1/registry/rename", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistrySkillCopy copies a skill with all versions to a new name.
func (c *Client) RegistrySkillCopy(name, newName string) (*RegistrySkillCopyResult, error) {
	req := struct {
		Name    string `json:"name"`
		NewName string `json:"new_name"`
	}{
		Name:    name,
		NewName: newName,
	}

	var result RegistrySkillCopyResult
	if err := c.post("/v1/registry/copy", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionCreate creates a new version for a skill.
func (c *Client) RegistryVersionCreate(name, description string, copyFromActive bool) (*RegistryVersionCreateResult, error) {
	req := struct {
		Name           string `json:"name"`
		Description    string `json:"description"`
		CopyFromActive bool   `json:"copy_from_active"`
	}{
		Name:           name,
		Description:    description,
		CopyFromActive: copyFromActive,
	}

	var result RegistryVersionCreateResult
	if err := c.post("/v1/registry/versions/create", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionClone clones an existing version's content into a new version.
func (c *Client) RegistryVersionClone(name, version, description string) (*RegistryVersionCloneResult, error) {
	return c.RegistryVersionCloneWithTarget(name, version, "", description)
}

// RegistryVersionCloneWithTarget clones an existing version's content into a new version.
// If newVersion is empty, the server allocates one using the default timestamp+random rule.
func (c *Client) RegistryVersionCloneWithTarget(name, version, newVersion, description string) (*RegistryVersionCloneResult, error) {
	req := struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		NewVersion  string `json:"new_version,omitempty"`
		Description string `json:"description"`
	}{
		Name:        name,
		Version:     version,
		NewVersion:  newVersion,
		Description: description,
	}

	var result RegistryVersionCloneResult
	if err := c.post("/v1/registry/versions/clone", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionGet retrieves a specific version's metadata and SKILLS.md content.
func (c *Client) RegistryVersionGet(name, version string) (*RegistryVersionGetResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{
		Name:    name,
		Version: version,
	}

	var result RegistryVersionGetResult
	if err := c.post("/v1/registry/versions/get", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionList lists all versions of a skill.
func (c *Client) RegistryVersionList(name string) (*RegistryVersionListResult, error) {
	req := struct {
		Name string `json:"name"`
	}{
		Name: name,
	}

	var result RegistryVersionListResult
	if err := c.post("/v1/registry/versions/list", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionDelete deletes a version (cannot delete the active version).
func (c *Client) RegistryVersionDelete(name, version string) error {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{
		Name:    name,
		Version: version,
	}

	return c.post("/v1/registry/versions/delete", req, nil)
}

// RegistryVersionTree returns the directory tree of a specific version.
func (c *Client) RegistryVersionTree(name, version string) (*SkillTreeResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{
		Name:    name,
		Version: version,
	}

	var result SkillTreeResult
	if err := c.post("/v1/registry/versions/tree", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionFileRead reads a file within a specific version.
func (c *Client) RegistryVersionFileRead(name, version, path string) (*SkillFileReadResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Path    string `json:"path"`
	}{
		Name:    name,
		Version: version,
		Path:    path,
	}

	var result SkillFileReadResult
	if err := c.post("/v1/registry/versions/file/read", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionFileWrite writes a file within a specific version.
func (c *Client) RegistryVersionFileWrite(name, version, path, content string) (*SkillFileWriteResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}{
		Name:    name,
		Version: version,
		Path:    path,
		Content: content,
	}

	var result SkillFileWriteResult
	if err := c.post("/v1/registry/versions/file/write", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionFileUpdate replaces string content in a file within a specific version.
func (c *Client) RegistryVersionFileUpdate(name, version, path, oldStr, newStr string) (*SkillFileUpdateResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Path    string `json:"path"`
		OldStr  string `json:"old_str"`
		NewStr  string `json:"new_str"`
	}{
		Name:    name,
		Version: version,
		Path:    path,
		OldStr:  oldStr,
		NewStr:  newStr,
	}

	var result SkillFileUpdateResult
	if err := c.post("/v1/registry/versions/file/update", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionFileMkdir creates a directory within a specific version.
func (c *Client) RegistryVersionFileMkdir(name, version, path string) (*SkillFileMkdirResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Path    string `json:"path"`
	}{
		Name:    name,
		Version: version,
		Path:    path,
	}

	var result SkillFileMkdirResult
	if err := c.post("/v1/registry/versions/file/mkdir", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryVersionFileDelete deletes a file or directory within a specific version.
func (c *Client) RegistryVersionFileDelete(name, version, path string) (*SkillFileDeleteResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Path    string `json:"path"`
	}{
		Name:    name,
		Version: version,
		Path:    path,
	}

	var result SkillFileDeleteResult
	if err := c.post("/v1/registry/versions/file/delete", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryActivate deploys a specific version to /data/skills/.
func (c *Client) RegistryActivate(name, version string) (*RegistryActivateResult, error) {
	req := struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}{
		Name:    name,
		Version: version,
	}

	var result RegistryActivateResult
	if err := c.post("/v1/registry/activate", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryCommit captures an agent's skill as a new version in the registry.
func (c *Client) RegistryCommit(name, agentID, description string, activate bool) (*RegistryCommitResult, error) {
	req := struct {
		Name        string `json:"name"`
		AgentID     string `json:"agent_id"`
		Description string `json:"description"`
		Activate    bool   `json:"activate"`
	}{
		Name:        name,
		AgentID:     agentID,
		Description: description,
		Activate:    activate,
	}

	var result RegistryCommitResult
	if err := c.post("/v1/registry/commit", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryImport imports a skill from a ZIP URL as a new version and auto-activates.
func (c *Client) RegistryImport(name, zipURL string) (*RegistryImportResult, error) {
	req := struct {
		Name   string `json:"name"`
		ZipURL string `json:"zip_url"`
	}{
		Name:   name,
		ZipURL: zipURL,
	}

	var result RegistryImportResult
	if err := c.post("/v1/registry/import", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryImportUpload imports skills from local ZIP files and auto-activates each.
func (c *Client) RegistryImportUpload(entries []SkillUploadEntry) (*RegistryImportUploadResult, error) {
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

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/v1/registry/import/upload", body)
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

	var result RegistryImportUploadResult
	if err := c.handleResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RegistryExport exports a specific version (or active version if empty) as a ZIP stream.
func (c *Client) RegistryExport(name string, version ...string) (io.ReadCloser, error) {
	u := c.baseURL + "/v1/registry/export?name=" + url.QueryEscape(name)
	if len(version) > 0 && version[0] != "" {
		u += "&version=" + url.QueryEscape(version[0])
	}

	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return nil, &Error{StatusCode: resp.StatusCode, Message: string(raw)}
	}

	return resp.Body, nil
}
