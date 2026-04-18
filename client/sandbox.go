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
