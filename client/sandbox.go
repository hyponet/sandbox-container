package client

// GetSandboxContext returns sandbox environment information.
func (c *Client) GetSandboxContext() (*SandboxResponse, error) {
	var result SandboxResponse
	if err := c.get("/v1/sandbox", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetPythonPackages returns installed Python packages.
// The result is a slice of package entries; the exact shape depends on pip's JSON output.
func (c *Client) GetPythonPackages() ([]PackageInfo, error) {
	var result []PackageInfo
	if err := c.get("/v1/sandbox/packages/python", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetNodejsPackages returns installed Node.js packages.
// The result is a slice of package entries; the exact shape depends on npm's JSON output.
func (c *Client) GetNodejsPackages() ([]PackageInfo, error) {
	var result []PackageInfo
	if err := c.get("/v1/sandbox/packages/nodejs", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// FsInfoOption is a functional option for GetFsInfo.
type FsInfoOption func(*fsInfoOptions)

type fsInfoOptions struct {
	EnableAgentWorkspace bool
	UserID              string
}

// WithFsInfoUserID sets the user ID for userdata access in GetFsInfo.
func WithFsInfoUserID(userID string) FsInfoOption {
	return func(o *fsInfoOptions) { o.UserID = userID }
}

// WithFsInfoAgentWorkspace enables agent workspace mode for the fsinfo request.
func WithFsInfoAgentWorkspace() FsInfoOption {
	return func(o *fsInfoOptions) {
		o.EnableAgentWorkspace = true
	}
}

// GetFsInfo returns filesystem layout information for a session.
func (c *Client) GetFsInfo(agentID, sessionID string, opts ...FsInfoOption) (*FsInfoResponse, error) {
	var o fsInfoOptions
	for _, opt := range opts {
		opt(&o)
	}

	reqBody := map[string]interface{}{
		"agent_id":   agentID,
		"session_id": sessionID,
	}
	if o.EnableAgentWorkspace {
		reqBody["enable_agent_workspace"] = true
	}
	if o.UserID != "" {
		reqBody["user_id"] = o.UserID
	}

	var result FsInfoResponse
	if err := c.post("/v1/sandbox/fsinfo", reqBody, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
