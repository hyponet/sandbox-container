package client

import (
	"fmt"
	"net/url"
)

// BashExec sends a synchronous or asynchronous bash command.
func (c *Client) BashExec(agentID, sessionID, command string, opts ...BashExecOption) (*BashExecResult, error) {
	req := bashExecRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Command:   command,
	}
	for _, o := range opts {
		o(&req)
	}

	var result BashExecResult
	if err := c.post("/v1/bash/exec", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BashOutput reads incremental output from a running command.
func (c *Client) BashOutput(agentID, sessionID, commandID string, offset, stderrOffset int, opts ...BashOutputOption) (*BashOutputResult, error) {
	req := bashOutputRequest{
		AgentID:      agentID,
		SessionID:    sessionID,
		CommandID:    nullableString(commandID),
		Offset:       offset,
		StderrOffset: stderrOffset,
	}
	for _, o := range opts {
		o(&req)
	}

	var result BashOutputResult
	if err := c.post("/v1/bash/output", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BashWrite writes input to a running process's stdin.
func (c *Client) BashWrite(agentID, sessionID, commandID, input string) error {
	req := bashWriteRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		CommandID: nullableString(commandID),
		Input:     input,
	}
	return c.post("/v1/bash/write", req, nil)
}

// BashKill kills the running process in a session.
func (c *Client) BashKill(agentID, sessionID, signal string) error {
	req := bashKillRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Signal:    signal,
	}
	return c.post("/v1/bash/kill", req, nil)
}

// BashCreateSession creates a new persistent bash session.
func (c *Client) BashCreateSession(agentID, sessionID string, opts ...BashCreateSessionOption) (*BashSessionInfo, error) {
	req := bashSessionCreateRequest{
		AgentID:   agentID,
		SessionID: sessionID,
	}
	for _, o := range opts {
		o(&req)
	}
	var result BashSessionInfo
	if err := c.post("/v1/bash/sessions/create", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BashListSessions lists all bash sessions for a given sandbox session.
func (c *Client) BashListSessions(sessionID string) ([]BashSessionInfo, error) {
	path := "/v1/bash/sessions?session_id=" + url.QueryEscape(sessionID)
	var result []BashSessionInfo
	if err := c.get(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// BashCloseSession closes a bash session.
func (c *Client) BashCloseSession(agentID, sessionID, bashSessionID string) error {
	req := bashSessionCloseRequest{
		AgentID:   agentID,
		SessionID: sessionID,
	}
	path := fmt.Sprintf("/v1/bash/sessions/%s/close", bashSessionID)
	return c.post(path, req, nil)
}

// --- Internal request types (with JSON tags for serialization) ---

type bashExecRequest struct {
	AgentID         string            `json:"agent_id"`
	SessionID       string            `json:"session_id"`
	Command         string            `json:"command"`
	ExecDir         *string           `json:"exec_dir,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	AsyncMode       bool              `json:"async_mode"`
	Timeout         *float64          `json:"timeout,omitempty"`
	HardTimeout     *float64          `json:"hard_timeout,omitempty"`
	MaxOutputLength      int               `json:"max_output_length"`
	EnableAgentWorkspace bool              `json:"enable_agent_workspace"`
	UserID               string            `json:"user_id,omitempty"`
}

type bashOutputRequest struct {
	AgentID      string  `json:"agent_id"`
	SessionID    string  `json:"session_id"`
	CommandID    *string `json:"command_id,omitempty"`
	Offset       int     `json:"offset"`
	StderrOffset int     `json:"stderr_offset"`
	Wait         bool    `json:"wait"`
	WaitTimeout  float64 `json:"wait_timeout"`
}

type bashWriteRequest struct {
	AgentID   string  `json:"agent_id"`
	SessionID string  `json:"session_id"`
	CommandID *string `json:"command_id,omitempty"`
	Input     string  `json:"input"`
}

type bashKillRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Signal    string `json:"signal"`
}

type bashSessionCreateRequest struct {
	AgentID   string  `json:"agent_id"`
	SessionID string  `json:"session_id"`
	BashSID              *string `json:"bash_session_id,omitempty"`
	ExecDir              *string `json:"exec_dir,omitempty"`
	EnableAgentWorkspace bool    `json:"enable_agent_workspace"`
	UserID               string  `json:"user_id,omitempty"`
}

type bashSessionCloseRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
}

// --- Functional options ---

// BashExecOption is a functional option for BashExec.
type BashExecOption func(*bashExecRequest)

// WithExecDir sets the working directory for the command.
func WithExecDir(dir string) BashExecOption {
	return func(r *bashExecRequest) { r.ExecDir = &dir }
}

// WithEnv sets environment variables for the command.
func WithEnv(env map[string]string) BashExecOption {
	return func(r *bashExecRequest) { r.Env = env }
}

// WithAsyncMode enables async execution.
func WithAsyncMode(async bool) BashExecOption {
	return func(r *bashExecRequest) { r.AsyncMode = async }
}

// WithTimeout sets the command timeout in seconds.
func WithTimeout(seconds float64) BashExecOption {
	return func(r *bashExecRequest) { r.Timeout = &seconds }
}

// WithHardTimeout sets the hard timeout in seconds (kills process after this duration).
func WithHardTimeout(seconds float64) BashExecOption {
	return func(r *bashExecRequest) { r.HardTimeout = &seconds }
}

// WithMaxOutputLength sets the maximum output length in bytes.
// A value of 0 (the default) means no limit.
func WithMaxOutputLength(length int) BashExecOption {
	return func(r *bashExecRequest) { r.MaxOutputLength = length }
}

// WithAgentWorkspace enables agent workspace mode for BashExec.
func WithAgentWorkspace() BashExecOption {
	return func(r *bashExecRequest) { r.EnableAgentWorkspace = true }
}

// BashCreateSessionOption is a functional option for BashCreateSession.
type BashCreateSessionOption func(*bashSessionCreateRequest)

// WithBashSID sets a custom bash session ID.
func WithBashSID(sid string) BashCreateSessionOption {
	return func(r *bashSessionCreateRequest) { r.BashSID = &sid }
}

// WithSessionExecDir sets the working directory for the bash session.
func WithSessionExecDir(dir string) BashCreateSessionOption {
	return func(r *bashSessionCreateRequest) { r.ExecDir = &dir }
}

// WithCreateSessionAgentWorkspace enables agent workspace mode for BashCreateSession.
func WithCreateSessionAgentWorkspace() BashCreateSessionOption {
	return func(r *bashSessionCreateRequest) { r.EnableAgentWorkspace = true }
}

// BashOutputOption is a functional option for BashOutput.
type BashOutputOption func(*bashOutputRequest)

// WithWait enables waiting for output with a timeout.
func WithWait(timeout float64) BashOutputOption {
	return func(r *bashOutputRequest) { r.Wait = true; r.WaitTimeout = timeout }
}

// WithBashUserID sets the user ID for userdata access in BashExec.
func WithBashUserID(userID string) BashExecOption {
	return func(r *bashExecRequest) { r.UserID = userID }
}

// WithCreateSessionUserID sets the user ID for userdata access in BashCreateSession.
func WithCreateSessionUserID(userID string) BashCreateSessionOption {
	return func(r *bashSessionCreateRequest) { r.UserID = userID }
}
