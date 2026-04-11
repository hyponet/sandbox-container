package client

// GetSandboxContext returns sandbox environment information.
func (c *Client) GetSandboxContext() (*SandboxResponse, error) {
	var result SandboxResponse
	if err := c.get("/v1/sandbox", &result); err != nil {
		return nil, err
	}
	return &result, nil
}
