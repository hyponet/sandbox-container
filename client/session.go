package client

import (
	"fmt"
	"net/url"
)

// SessionList returns all sessions for a given agent.
func (c *Client) SessionList(agentID string) (*SessionListResult, error) {
	params := url.Values{"agent_id": {agentID}}
	path := "/v1/sessions?" + params.Encode()
	var result SessionListResult
	if err := c.get(path, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SessionGetAuditLogs returns paginated audit log entries for a session.
func (c *Client) SessionGetAuditLogs(agentID, sessionID string, offset, limit int) (*AuditLogResult, error) {
	params := url.Values{
		"agent_id": {agentID},
		"offset":   {fmt.Sprintf("%d", offset)},
		"limit":    {fmt.Sprintf("%d", limit)},
	}
	path := fmt.Sprintf("/v1/sessions/%s/audits?%s", url.PathEscape(sessionID), params.Encode())
	var result AuditLogResult
	if err := c.get(path, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SessionDelete removes a session and its audit logs.
func (c *Client) SessionDelete(agentID, sessionID string) error {
	path := fmt.Sprintf("/v1/sessions/%s?agent_id=%s", url.PathEscape(sessionID), url.QueryEscape(agentID))
	return c.doJSON("DELETE", path, nil, nil)
}
