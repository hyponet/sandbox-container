package client

import (
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const maxErrorBody = 1024 * 1024 // 1MB limit for error response bodies

// SkillUploadEntry pairs a skill name with a local ZIP file path for upload.
type SkillUploadEntry struct {
	Name    string // Skill ID (letters, digits, hyphens)
	ZipPath string // Local path to the ZIP file
}

// AgentSkillOption is a functional option for SkillAgentList and SkillAgentLoad.
type AgentSkillOption func(*agentSkillRequest)

type agentSkillRequest struct {
	SkillIDs            []string `json:"skill_ids"`
	Cleanup             bool     `json:"cleanup"`
	EnableAgentWorkspace bool    `json:"enable_agent_workspace"`
}

// WithCleanup enables cleanup of skills not in the requested list.
func WithCleanup() AgentSkillOption {
	return func(r *agentSkillRequest) { r.Cleanup = true }
}

// WithAgentSkillWorkspace enables agent workspace mode for agent skill operations.
func WithAgentSkillWorkspace() AgentSkillOption {
	return func(r *agentSkillRequest) { r.EnableAgentWorkspace = true }
}

// SkillAgentList syncs skills to agent cache and returns frontmatter summaries.
// Results are cached locally (default 5 min, configurable via WithAgentListCacheTTL).
// Within the cache window, repeated calls with the same agentID + skillIDs + options
// return the cached result without contacting the server, so server-side skill
// changes will not be visible until the entry expires or is invalidated.
func (c *Client) SkillAgentList(agentID string, skillIDs []string, opts ...AgentSkillOption) (*AgentSkillListResult, error) {
	req := agentSkillRequest{SkillIDs: skillIDs}
	for _, o := range opts {
		o(&req)
	}

	key := agentListCacheKey(agentID, &req)

	// Fast path: return a copy from cache if still valid.
	c.agentListMu.Lock()
	if entry, ok := c.agentListCache[key]; ok && time.Now().Before(entry.expiresAt) {
		copied := copyAgentSkillListResult(entry.result)
		c.agentListMu.Unlock()
		return copied, nil
	}
	c.agentListMu.Unlock()

	// Slow path: use singleflight to coalesce concurrent misses for the same key.
	v, err, _ := c.agentListFlight.Do(key, func() (interface{}, error) {
		var result AgentSkillListResult
		if err := c.post("/v1/skills/agents/"+agentID+"/list", req, &result); err != nil {
			return nil, err
		}

		c.agentListMu.Lock()
		c.evictExpiredAgentListCacheLocked()
		c.agentListCache[key] = &agentListCacheEntry{
			result:    &result,
			expiresAt: time.Now().Add(c.agentListTTL),
		}
		c.agentListMu.Unlock()

		return &result, nil
	})
	if err != nil {
		return nil, err
	}

	return copyAgentSkillListResult(v.(*AgentSkillListResult)), nil
}

// copyAgentSkillListResult returns a shallow copy of the result with its own Skills slice
// so callers cannot mutate the cached data.
func copyAgentSkillListResult(src *AgentSkillListResult) *AgentSkillListResult {
	dst := *src
	dst.Skills = make([]SkillSummary, len(src.Skills))
	copy(dst.Skills, src.Skills)
	return &dst
}

// SkillAgentLoad syncs skills to agent cache and returns SKILLS.md body content.
func (c *Client) SkillAgentLoad(agentID string, skillIDs []string, opts ...AgentSkillOption) (*AgentSkillLoadResult, error) {
	req := agentSkillRequest{SkillIDs: skillIDs}
	for _, o := range opts {
		o(&req)
	}

	var result AgentSkillLoadResult
	if err := c.post("/v1/skills/agents/"+agentID+"/load", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SkillAgentCacheDelete deletes cached skills for an agent.
// On success it also invalidates the local SkillAgentList cache for the given agent.
func (c *Client) SkillAgentCacheDelete(agentID string, skillID ...string) (*AgentSkillCacheDeleteResult, error) {
	path := "/v1/skills/agents/" + agentID + "/cache"
	if len(skillID) > 0 && skillID[0] != "" {
		path += "?skill_id=" + url.QueryEscape(skillID[0])
	}

	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
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
	defer resp.Body.Close()

	var result AgentSkillCacheDeleteResult
	if err := c.handleResponse(resp, &result); err != nil {
		return nil, err
	}

	// Invalidate local cache only after the server-side delete succeeds.
	c.invalidateAgentListCache(agentID)

	return &result, nil
}
