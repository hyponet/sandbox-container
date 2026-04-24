package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func setupCodeRouter() (*gin.Engine, *session.Manager) {
	return setupCodeRouterWithExecutor(&executor.DirectExecutor{})
}

func setupCodeRouterWithExecutor(cmdExec executor.CommandExecutor) (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), "sandbox-code-test-"+time.Now().Format("20060102150405"))
	os.MkdirAll(dir, 0755)
	mgr := session.NewManager(dir, 24*time.Hour)
	mgr.SetSessionInit(cmdExec.InitSession)

	r := gin.New()
	codeH := NewCodeHandler(mgr, cmdExec, false)
	r.POST("/v1/code/execute", codeH.Execute)
	r.GET("/v1/code/info", codeH.Info)

	return r, mgr
}

func TestCodeExecutePython(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"agent_id": "a1", "session_id": "code1", "language": "python", "code": "print(2+2)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout != "4\n" {
		t.Errorf("expected '4\\n', got %q", stdout)
	}
}

func TestCodeExecuteJavaScript(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"agent_id": "a1", "session_id": "code2", "language": "javascript", "code": "console.log(2+2)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout != "4\n" {
		t.Errorf("expected '4\\n', got %q", stdout)
	}
}

func TestCodeExecuteNoSessionID(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"language": "python", "code": "print(1)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestCodeExecuteUnsupportedLang(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"agent_id": "a1", "session_id": "code3", "language": "rust", "code": "fn main() {}"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for unsupported language, got %d", w.Code)
	}
}

func TestCodeInfo(t *testing.T) {
	r, _ := setupCodeRouter()

	req := httptest.NewRequest(http.MethodGet, "/v1/code/info", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("info failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	langs := data["languages"].([]interface{})
	if len(langs) < 2 {
		t.Errorf("expected at least 2 languages, got %d", len(langs))
	}
}

func TestCodeExecuteJSAlias(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"agent_id": "a1", "session_id": "code4", "language": "js", "code": "console.log(42)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("js alias execute failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout != "42\n" {
		t.Errorf("expected '42\\n', got %q", stdout)
	}
}

func TestCodeExecuteError(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"agent_id": "a1", "session_id": "code5", "language": "python", "code": "raise ValueError('test error')"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("error execute should return 200 with error info, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})

	stderr, _ := data["stderr"].(string)
	if stderr == "" {
		t.Error("expected non-empty stderr for failed code")
	}

	exitCode := int(data["exit_code"].(float64))
	if exitCode == 0 {
		t.Error("expected non-zero exit code for failed code")
	}

	traceback, _ := data["traceback"].([]interface{})
	if len(traceback) == 0 {
		t.Error("expected traceback for failed code")
	}
}

func TestCodeExecuteMissingLanguage(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"agent_id": "a1", "session_id": "code6", "code": "print(1)"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing language, got %d", w.Code)
	}
}

func TestCodeExecute_AgentWorkspace(t *testing.T) {
	r, mgr := setupCodeRouter()

	// Execute with enable_agent_workspace — working dir should be workspace root
	body := `{"agent_id": "a1", "session_id": "code_ws", "language": "python", "code": "import os; print(os.getcwd())", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout, _ := data["stdout"].(string)

	wsRoot := mgr.WorkspaceRoot("a1")
	if !strings.Contains(stdout, filepath.Base(wsRoot)) {
		t.Errorf("expected stdout to reference workspace dir, got %q", stdout)
	}
}

func TestCodeExecutePWDMatchesWorkingDir(t *testing.T) {
	r, _ := setupCodeRouter()

	body := `{"agent_id": "a1", "session_id": "code_pwd", "language": "python", "code": "import os; print('match' if os.getcwd() == os.environ.get('PWD') else 'mismatch')", "cwd": "/subdir"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/code/execute", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("execute failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	stdout := data["stdout"].(string)
	if stdout != "match\n" {
		t.Errorf("expected PWD to match cwd, got %q", stdout)
	}
}
