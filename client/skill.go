package client

// SkillList downloads skill ZIPs from URLs, extracts them, and returns metadata.
func (c *Client) SkillList(agentID string, skillURLs []string) (*SkillListResult, error) {
	req := skillListRequest{
		AgentID:   agentID,
		SkillURLs: skillURLs,
	}

	var result SkillListResult
	if err := c.post("/v1/skills/list", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillLoad returns the SKILLS.MD/SKILL.md content for requested skill names.
func (c *Client) SkillLoad(agentID string, skillNames []string) (*SkillLoadResult, error) {
	req := skillLoadRequest{
		AgentID:    agentID,
		SkillNames: skillNames,
	}

	var result SkillLoadResult
	if err := c.post("/v1/skills/load", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Internal request types ---

type skillListRequest struct {
	AgentID   string   `json:"agent_id"`
	SkillURLs []string `json:"skill_urls"`
}

type skillLoadRequest struct {
	AgentID    string   `json:"agent_id"`
	SkillNames []string `json:"skill_names"`
}
