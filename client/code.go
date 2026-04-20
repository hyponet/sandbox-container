package client

// CodeExecute executes code in a sandbox runtime.
func (c *Client) CodeExecute(agentID, sessionID, language, code string, opts ...CodeExecOption) (*CodeExecuteResponse, error) {
	req := codeExecRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Language:  language,
		Code:      code,
	}
	for _, o := range opts {
		o(&req)
	}

	var result CodeExecuteResponse
	if err := c.post("/v1/code/execute", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CodeInfo returns available language runtimes.
func (c *Client) CodeInfo() (*CodeInfoResponse, error) {
	var result CodeInfoResponse
	if err := c.get("/v1/code/info", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Internal request types ---

type codeExecRequest struct {
	AgentID              string            `json:"agent_id"`
	SessionID            string            `json:"session_id"`
	Language             string            `json:"language"`
	Code                 string            `json:"code"`
	Timeout              *int              `json:"timeout,omitempty"`
	Cwd                  *string           `json:"cwd,omitempty"`
	Env                  map[string]string `json:"env,omitempty"`
	EnableAgentWorkspace bool              `json:"enable_agent_workspace"`
}

// --- Functional options ---

// CodeExecOption is a functional option for CodeExecute.
type CodeExecOption func(*codeExecRequest)

// WithCodeTimeout sets the execution timeout in seconds.
func WithCodeTimeout(seconds int) CodeExecOption {
	return func(r *codeExecRequest) { r.Timeout = &seconds }
}

// WithCwd sets the working directory for code execution.
func WithCwd(cwd string) CodeExecOption {
	return func(r *codeExecRequest) { r.Cwd = &cwd }
}

// WithCodeEnv sets environment variables for code execution.
func WithCodeEnv(env map[string]string) CodeExecOption {
	return func(r *codeExecRequest) { r.Env = env }
}

// WithCodeAgentWorkspace enables agent workspace mode for CodeExecute.
func WithCodeAgentWorkspace() CodeExecOption {
	return func(r *codeExecRequest) { r.EnableAgentWorkspace = true }
}
