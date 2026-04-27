package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hyponet/sandbox-container/executor"
)

type captureExecutor struct {
	opts executor.ExecOptions
	name string
	args []string
}

func (c *captureExecutor) Prepare(opts executor.ExecOptions, name string, args ...string) *exec.Cmd {
	c.opts = cloneExecOptions(opts)
	c.name = name
	c.args = append([]string(nil), args...)

	cmd := exec.CommandContext(opts.Ctx, "sh", "-c", "printf ''")
	cmd.Dir = opts.WorkingDir
	cmd.Env = append([]string(nil), opts.Env...)
	return cmd
}

func (c *captureExecutor) InitSession(sessionDir, skillsDir string) {}

func (c *captureExecutor) InitUserdata(sessionDir, userdataDir string) {}

func cloneExecOptions(opts executor.ExecOptions) executor.ExecOptions {
	opts.Env = append([]string(nil), opts.Env...)
	if opts.RWBinds != nil {
		opts.RWBinds = append([]executor.BindMount(nil), opts.RWBinds...)
	}
	if opts.ROBinds != nil {
		opts.ROBinds = append([]executor.BindMount(nil), opts.ROBinds...)
	}
	return opts
}

func containsExecPath(binds []executor.BindMount, want string) bool {
	cleanWant := filepath.Clean(want)
	for _, bm := range binds {
		if filepath.Clean(bm.Src) == cleanWant {
			return true
		}
	}
	return false
}

func TestBashExecBindModes(t *testing.T) {
	tests := []struct {
		name                  string
		body                  string
		sessionID             string
		wantWorkspaceWritable bool
	}{
		{
			name:                  "session mode keeps skills read-only",
			body:                  `{"agent_id":"a1","session_id":"bash-session","command":"true"}`,
			sessionID:             "bash-session",
			wantWorkspaceWritable: false,
		},
		{
			name:                  "workspace mode makes skills writable",
			body:                  `{"agent_id":"a1","session_id":"bash-workspace","command":"true","enable_agent_workspace":true}`,
			sessionID:             "bash-workspace",
			wantWorkspaceWritable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdExec := &captureExecutor{}
			r, mgr := setupBashRouterWithExecutor(cmdExec)

			req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("exec failed: %d %s", w.Code, w.Body.String())
			}

			skillsRoot := mgr.SkillsRoot("a1")
			if tt.wantWorkspaceWritable {
				if !containsExecPath(cmdExec.opts.RWBinds, mgr.WorkspaceRoot("a1")) {
					t.Fatalf("expected workspace root to be writable, got %v", cmdExec.opts.RWBinds)
				}
				if !containsExecPath(cmdExec.opts.RWBinds, skillsRoot) {
					t.Fatalf("expected skills root to be writable in workspace mode, got %v", cmdExec.opts.RWBinds)
				}
				if containsExecPath(cmdExec.opts.ROBinds, skillsRoot) {
					t.Fatalf("did not expect skills root to remain read-only in workspace mode, got %v", cmdExec.opts.ROBinds)
				}
				return
			}

			if !containsExecPath(cmdExec.opts.RWBinds, mgr.SessionRoot("a1", tt.sessionID)) {
				t.Fatalf("expected session root to be writable, got %v", cmdExec.opts.RWBinds)
			}
			if containsExecPath(cmdExec.opts.RWBinds, skillsRoot) {
				t.Fatalf("did not expect skills root to be writable outside workspace mode, got %v", cmdExec.opts.RWBinds)
			}
			if !containsExecPath(cmdExec.opts.ROBinds, skillsRoot) {
				t.Fatalf("expected skills root to be read-only outside workspace mode, got %v", cmdExec.opts.ROBinds)
			}
		})
	}
}

func TestCodeExecuteBindModes(t *testing.T) {
	tests := []struct {
		name                  string
		body                  string
		sessionID             string
		wantWorkspaceWritable bool
	}{
		{
			name:                  "session mode keeps skills read-only",
			body:                  `{"agent_id":"a1","session_id":"code-session","language":"python","code":"print(1)"}`,
			sessionID:             "code-session",
			wantWorkspaceWritable: false,
		},
		{
			name:                  "workspace mode makes skills writable",
			body:                  `{"agent_id":"a1","session_id":"code-workspace","language":"python","code":"print(1)","enable_agent_workspace":true}`,
			sessionID:             "code-workspace",
			wantWorkspaceWritable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmdExec := &captureExecutor{}
			r, mgr := setupCodeRouterWithExecutor(cmdExec)

			req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("execute failed: %d %s", w.Code, w.Body.String())
			}

			skillsRoot := mgr.SkillsRoot("a1")
			if tt.wantWorkspaceWritable {
				if !containsExecPath(cmdExec.opts.RWBinds, mgr.WorkspaceRoot("a1")) {
					t.Fatalf("expected workspace root to be writable, got %v", cmdExec.opts.RWBinds)
				}
				if !containsExecPath(cmdExec.opts.RWBinds, skillsRoot) {
					t.Fatalf("expected skills root to be writable in workspace mode, got %v", cmdExec.opts.RWBinds)
				}
				if containsExecPath(cmdExec.opts.ROBinds, skillsRoot) {
					t.Fatalf("did not expect skills root to remain read-only in workspace mode, got %v", cmdExec.opts.ROBinds)
				}
				return
			}

			if !containsExecPath(cmdExec.opts.RWBinds, mgr.SessionRoot("a1", tt.sessionID)) {
				t.Fatalf("expected session root to be writable, got %v", cmdExec.opts.RWBinds)
			}
			if containsExecPath(cmdExec.opts.RWBinds, skillsRoot) {
				t.Fatalf("did not expect skills root to be writable outside workspace mode, got %v", cmdExec.opts.RWBinds)
			}
			if !containsExecPath(cmdExec.opts.ROBinds, skillsRoot) {
				t.Fatalf("expected skills root to be read-only outside workspace mode, got %v", cmdExec.opts.ROBinds)
			}
		})
	}
}
