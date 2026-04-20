package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func setupBashRouter() (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), "sandbox-bash-test-"+time.Now().Format("20060102150405"))
	os.MkdirAll(dir, 0755)
	mgr := session.NewManager(dir, 24*time.Hour)

	r := gin.New()
	bashH := NewBashHandler(mgr)
	bash := r.Group("/v1/bash")
	{
		bash.POST("/exec", bashH.Exec)
		bash.POST("/output", bashH.Output)
		bash.POST("/write", bashH.Write)
		bash.POST("/kill", bashH.Kill)
		bash.POST("/sessions/create", bashH.CreateSession)
		bash.GET("/sessions", bashH.ListSessions)
	}

	return r, mgr
}

func TestBashExecSimple(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash1", "command": "echo hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("exec failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", stdout)
	}
}

func TestBashExecMultiLine(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash2", "command": "echo line1 && echo line2"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout != "line1\nline2\n" {
		t.Errorf("expected multi-line output, got %q", stdout)
	}
}

func TestBashExecWorkdir(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash3", "command": "pwd"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout == "" {
		t.Error("pwd returned empty")
	}
}

func TestBashExecEnv(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash4", "command": "echo $MY_VAR", "env": {"MY_VAR": "test_value"}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout != "test_value\n" {
		t.Errorf("expected 'test_value\\n', got %q", stdout)
	}
}

func TestBashExecAsync(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash5", "command": "sleep 0.1 && echo done", "async_mode": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	status := data["status"].(string)
	if status != "running" {
		t.Errorf("async exec should return running, got %s", status)
	}
}

func TestBashCreateSession(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash6"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/sessions/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create session failed: %d %s", w.Code, w.Body.String())
	}
}

func TestBashExecExitCode(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash7", "command": "exit 42"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	exitCode := int(data["exit_code"].(float64))
	if exitCode != 42 {
		t.Errorf("expected exit code 42, got %d", exitCode)
	}
}

func TestBashExecNoSessionID(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"command": "echo hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestBashAccessSkillsDir(t *testing.T) {
	r, mgr := setupBashRouter()

	// Create a skill file
	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "test-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "test-skill", "hello.sh"), []byte("echo from-skill"), 0755)

	// Bash should be able to access skills via symlink
	body := `{"agent_id": "a1", "session_id": "bash8", "command": "ls skills/test-skill/hello.sh"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if !bytes.Contains([]byte(stdout), []byte("hello.sh")) {
		t.Errorf("expected to find hello.sh in skills listing, got %q", stdout)
	}
}

func TestBashOutput(t *testing.T) {
	r, _ := setupBashRouter()

	// Start an async command
	body := `{"agent_id": "a1", "session_id": "bash9", "command": "echo output_data", "async_mode": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var execResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &execResp)
	execData := execResp["data"].(map[string]interface{})
	commandID := execData["command_id"].(string)

	// Wait briefly for command to complete
	time.Sleep(200 * time.Millisecond)

	// Get output
	outputBody := fmt.Sprintf(`{"agent_id": "a1", "session_id": "bash9", "command_id": "%s"}`, commandID)
	req = httptest.NewRequest(http.MethodPost, "/v1/bash/output", bytes.NewBufferString(outputBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("output failed: %d %s", w.Code, w.Body.String())
	}

	var outputResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &outputResp)
	data := outputResp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if !bytes.Contains([]byte(stdout), []byte("output_data")) {
		t.Errorf("expected 'output_data' in stdout, got %q", stdout)
	}
}

func TestBashOutputSessionNotFound(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "nonexistent", "command_id": "x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/output", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent session, got %d", w.Code)
	}
}

func TestBashKill(t *testing.T) {
	r, _ := setupBashRouter()

	// Start a long-running async command
	body := `{"agent_id": "a1", "session_id": "bash10", "command": "sleep 30", "async_mode": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("exec failed: %d %s", w.Code, w.Body.String())
	}

	// Kill it
	killBody := `{"agent_id": "a1", "session_id": "bash10", "signal": "SIGKILL"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/bash/kill", bytes.NewBufferString(killBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("kill failed: %d %s", w.Code, w.Body.String())
	}

	var killResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &killResp)
	data := killResp["data"].(map[string]interface{})
	status := data["status"].(string)
	if status != "killed" {
		t.Errorf("expected status 'killed', got %s", status)
	}
}

func TestBashKillNonexistentSession(t *testing.T) {
	r, _ := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "nope", "signal": "SIGKILL"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/kill", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for killing nonexistent session, got %d", w.Code)
	}
}

func TestBashListSessions(t *testing.T) {
	r, _ := setupBashRouter()

	// Create a session
	createBody := `{"agent_id": "a1", "session_id": "bash11"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/sessions/create", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create session failed: %d %s", w.Code, w.Body.String())
	}

	// List sessions
	req = httptest.NewRequest(http.MethodGet, "/v1/bash/sessions?session_id=bash11", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list sessions failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].([]interface{})
	if len(data) < 1 {
		t.Errorf("expected at least 1 session, got %d", len(data))
	}
}

func TestBashExec_AgentWorkspace(t *testing.T) {
	r, mgr := setupBashRouter()

	body := `{"agent_id": "a1", "session_id": "bash_dsi", "command": "pwd", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("exec with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)

	// The working directory should be the workspace root, not a sessions path
	wsRoot := mgr.WorkspaceRoot("a1")
	if !strings.Contains(stdout, wsRoot) {
		t.Errorf("expected stdout to contain workspace path %q, got %q", wsRoot, stdout)
	}
	if strings.Contains(stdout, "sessions") {
		t.Errorf("stdout should NOT contain 'sessions' when enable_agent_workspace is true, got %q", stdout)
	}
}

func TestBashExec_AgentWorkspace_Persistence(t *testing.T) {
	r, _ := setupBashRouter()

	// Create a file with session1 in workspace mode
	body := `{"agent_id": "a1", "session_id": "ws_sess1", "command": "echo persistent-data > ws-persist.txt", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create file in workspace mode failed: %d %s", w.Code, w.Body.String())
	}

	// Read the file with a different session (session2) in workspace mode
	body = `{"agent_id": "a1", "session_id": "ws_sess2", "command": "cat ws-persist.txt", "enable_agent_workspace": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/bash/exec", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("read file from different session in workspace mode failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)

	if !strings.Contains(stdout, "persistent-data") {
		t.Errorf("expected stdout to contain 'persistent-data', got %q", stdout)
	}
}
