package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func setupSessionRouter() (*gin.Engine, *session.Manager, *audit.Writer, string) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), "sandbox-session-test-"+time.Now().Format("20060102150405.000000"))
	os.MkdirAll(dir, 0755)

	// Use a fallback dir inside the temp dir to avoid writing to /var/log/sandbox during tests
	fallbackDir := filepath.Join(dir, "fallback-logs")
	auditW := audit.NewWriterWithFallback(dir, time.Minute, fallbackDir)
	mgr := session.NewManager(dir, time.Hour)
	mgr.SetAuditWriter(auditW)

	r := gin.New()
	sessionH := NewSessionHandler(mgr)
	sess := r.Group("/v1/sessions")
	{
		sess.GET("", sessionH.ListSessions)
		sess.GET("/:session_id/audits", sessionH.GetAuditLogs)
		sess.DELETE("/:session_id", sessionH.DeleteSession)
	}

	return r, mgr, auditW, dir
}

func TestListSessionsEmpty(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions?agent_id=a1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp model.APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp.Success {
		t.Errorf("expected success")
	}
}

func TestListSessionsMissingAgentID(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestListSessionsWithSessions(t *testing.T) {
	r, mgr, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	// Create sessions
	mgr.Touch("a1", "sess1")
	mgr.Touch("a1", "sess2")

	// Write some audit entries
	auditW.WriteEntry("a1", "sess1", map[string]string{"msg": "entry1"})
	auditW.WriteEntry("a1", "sess1", map[string]string{"msg": "entry2"})
	auditW.WriteEntry("a1", "sess2", map[string]string{"msg": "entry3"})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions?agent_id=a1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp model.APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("response data is not a map")
	}
	sessions := data["sessions"].([]interface{})
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}

	total := int(data["total"].(float64))
	if total != 2 {
		t.Errorf("expected total 2, got %d", total)
	}
}

func TestGetAuditLogs(t *testing.T) {
	r, mgr, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	mgr.Touch("a1", "sess1")

	for i := 0; i < 5; i++ {
		entry := map[string]interface{}{
			"msg":   "test",
			"index": i,
		}
		auditW.WriteEntry("a1", "sess1", entry)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits?agent_id=a1&offset=1&limit=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp model.APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatal("response data is not a map")
	}

	entries := data["entries"].([]interface{})
	if len(entries) != 2 {
		t.Errorf("expected 2 entries (limit=2), got %d", len(entries))
	}

	total := int(data["total"].(float64))
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}

	offset := int(data["offset"].(float64))
	if offset != 1 {
		t.Errorf("expected offset 1, got %d", offset)
	}
}

func TestGetAuditLogsNotFound(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/nonexistent/audits?agent_id=a1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestGetAuditLogsMissingAgentID(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestDeleteSession(t *testing.T) {
	r, mgr, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	mgr.Touch("a1", "sess1")
	auditW.WriteEntry("a1", "sess1", map[string]string{"msg": "test"})

	if !mgr.Exists("a1", "sess1") {
		t.Fatal("session should exist before delete")
	}

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess1?agent_id=a1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	if mgr.Exists("a1", "sess1") {
		t.Error("session should not exist after delete")
	}

	auditPath := filepath.Join(dir, "a1", "audits", "sess1.jsonl")
	if _, err := os.Stat(auditPath); !os.IsNotExist(err) {
		t.Error("audit file should be deleted")
	}
}

func TestDeleteSessionNotFound(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/nonexistent?agent_id=a1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestDeleteSessionMissingAgentID(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestGetAuditLogsDefaultPagination(t *testing.T) {
	r, mgr, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	mgr.Touch("a1", "sess1")

	for i := 0; i < 150; i++ {
		auditW.WriteEntry("a1", "sess1", map[string]interface{}{"index": i})
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits?agent_id=a1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp model.APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp.Data.(map[string]interface{})
	entries := data["entries"].([]interface{})
	if len(entries) != 100 {
		t.Errorf("expected 100 entries (default limit), got %d", len(entries))
	}
	total := int(data["total"].(float64))
	if total != 150 {
		t.Errorf("expected total 150, got %d", total)
	}
}

func TestCountLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")
	f, _ := os.Create(path)
	for i := 0; i < 10; i++ {
		f.WriteString(`{"line":` + string(rune('0'+i)) + "}\n")
	}
	f.Close()

	count, err := countLines(path)
	if err != nil {
		t.Fatalf("countLines failed: %v", err)
	}
	if count != 10 {
		t.Errorf("expected 10 lines, got %d", count)
	}
}

func TestCountLinesNonexistent(t *testing.T) {
	_, err := countLines("/nonexistent/file.jsonl")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestAuditLogsJSONLFormat(t *testing.T) {
	r, mgr, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	mgr.Touch("a1", "sess1")

	auditW.WriteEntry("a1", "sess1", map[string]interface{}{
		"method": "POST",
		"path":   "/v1/bash/exec",
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits?agent_id=a1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp model.APIResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp.Data.(map[string]interface{})
	entries := data["entries"].([]interface{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	entry := entries[0].(map[string]interface{})
	if entry["method"] != "POST" {
		t.Errorf("expected method POST, got %v", entry["method"])
	}
	if entry["path"] != "/v1/bash/exec" {
		t.Errorf("expected path /v1/bash/exec, got %v", entry["path"])
	}
}

func TestSetupSessionRouterFormat(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions?agent_id=test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if err := json.Unmarshal(w.Body.Bytes(), &map[string]interface{}{}); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
}

// Path traversal validation tests

func TestListSessionsInvalidAgentID(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions?agent_id=../../etc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal agent_id, got %d", w.Code)
	}
}

func TestGetAuditLogsInvalidAgentID(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits?agent_id=../evil", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal agent_id, got %d", w.Code)
	}
}

func TestDeleteSessionInvalidAgentID(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	req := httptest.NewRequest(http.MethodDelete, "/v1/sessions/sess1?agent_id=../evil", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for path traversal agent_id, got %d", w.Code)
	}
}

func TestGetAuditLogsInvalidPagination(t *testing.T) {
	r, _, auditW, dir := setupSessionRouter()
	defer auditW.Close()
	defer os.RemoveAll(dir)

	// Invalid offset
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits?agent_id=a1&offset=abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid offset, got %d", w.Code)
	}

	// Invalid limit
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits?agent_id=a1&limit=-1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid limit, got %d", w.Code)
	}

	// Limit too large
	req = httptest.NewRequest(http.MethodGet, "/v1/sessions/sess1/audits?agent_id=a1&limit=9999", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for limit > 1000, got %d", w.Code)
	}
}

func TestAuditWriterValidateID(t *testing.T) {
	if err := audit.ValidateID(""); err == nil {
		t.Error("expected error for empty ID")
	}
	if err := audit.ValidateID("../evil"); err == nil {
		t.Error("expected error for path traversal ID")
	}
	if err := audit.ValidateID("a/b"); err == nil {
		t.Error("expected error for ID with slash")
	}
	if err := audit.ValidateID("."); err == nil {
		t.Error("expected error for dot ID")
	}
	if err := audit.ValidateID("valid-id-123"); err != nil {
		t.Errorf("expected no error for valid ID, got %v", err)
	}
}
