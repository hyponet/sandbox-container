package client

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

// SkillLoad loads skills into an agent's local cache and returns their content.
func (c *Client) SkillLoad(agentID string, skillIDs []string) (*SkillLoadResult, error) {
	req := struct {
		AgentID  string   `json:"agent_id"`
		SkillIDs []string `json:"skill_ids"`
	}{
		AgentID:  agentID,
		SkillIDs: skillIDs,
	}

	var result SkillLoadResult
	if err := c.post("/v1/skills/load", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
