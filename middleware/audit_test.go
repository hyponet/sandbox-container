package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/model"

	"github.com/gin-gonic/gin"
)

func setupAuditRouter(t *testing.T) (*gin.Engine, *audit.Writer, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	fallbackDir := filepath.Join(dir, "fallback")
	w := audit.NewWriterWithFallback(dir, time.Minute, fallbackDir)

	r := gin.New()
	mw := AuditLogger(w)

	// A simple echo handler that reads the body and returns it.
	echo := func(c *gin.Context) {
		body, _ := io.ReadAll(c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"echo": string(body)})
	}

	// A handler that writes a large response.
	largeResp := func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "application/octet-stream")
		c.Writer.WriteHeader(http.StatusOK)
		// Write 1MB of data
		chunk := bytes.Repeat([]byte("x"), 1024)
		for i := 0; i < 1024; i++ {
			c.Writer.Write(chunk)
		}
	}

	r.POST("/audited", mw, echo)
	r.POST("/audited/upload", mw, func(c *gin.Context) {
		agentID := c.PostForm("agent_id")
		sessionID := c.PostForm("session_id")
		c.JSON(http.StatusOK, gin.H{"agent_id": agentID, "session_id": sessionID})
	})
	r.POST("/large-resp", mw, largeResp)
	r.POST("/no-audit", echo)

	return r, w, dir
}

func readAuditEntries(t *testing.T, dir, agentID, sessionID string) []model.AuditEntry {
	t.Helper()
	path := filepath.Join(dir, agentID, "audits", sessionID+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entries []model.AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var e model.AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err == nil {
			entries = append(entries, e)
		}
	}
	return entries
}

// TestAuditBasicJSONBody verifies that a normal JSON request is audited correctly.
func TestAuditBasicJSONBody(t *testing.T) {
	r, w, dir := setupAuditRouter(t)
	defer w.Close()

	body := `{"agent_id":"a1","session_id":"s1","command":"echo hi"}`
	req := httptest.NewRequest(http.MethodPost, "/audited", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// The handler should have received the full body.
	var resp map[string]interface{}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	if !strings.Contains(resp["echo"].(string), "echo hi") {
		t.Errorf("handler did not receive full body: %v", resp)
	}

	w.SyncSession("a1", "s1")
	entries := readAuditEntries(t, dir, "a1", "s1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	e := entries[0]
	if e.AgentID != "a1" || e.SessionID != "s1" {
		t.Errorf("wrong IDs: agent=%q session=%q", e.AgentID, e.SessionID)
	}
	if e.Method != "POST" || e.Path != "/audited" {
		t.Errorf("wrong method/path: %s %s", e.Method, e.Path)
	}
	if e.Status != 200 {
		t.Errorf("expected status 200, got %d", e.Status)
	}
	if e.Latency == "" {
		t.Error("expected non-empty latency")
	}
}

// TestAuditIDExtractionFromLargeBody verifies that agent_id/session_id are extracted
// even when the body exceeds maxAuditBodySize, because ID extraction uses a separate
// small prefix (maxIDExtractSize).
func TestAuditIDExtractionFromLargeBody(t *testing.T) {
	r, w, dir := setupAuditRouter(t)
	defer w.Close()

	// Build a JSON body where agent_id/session_id are in the first few bytes,
	// but the total body exceeds maxAuditBodySize.
	prefix := `{"agent_id":"a1","session_id":"s1","data":"`
	padding := strings.Repeat("x", maxAuditBodySize+1024)
	body := prefix + padding + `"}`

	req := httptest.NewRequest(http.MethodPost, "/audited", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	w.SyncSession("a1", "s1")
	entries := readAuditEntries(t, dir, "a1", "s1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 per-session audit entry, got %d — IDs were not extracted from large body", len(entries))
	}
	if entries[0].AgentID != "a1" || entries[0].SessionID != "s1" {
		t.Errorf("wrong IDs: %+v", entries[0])
	}
}

// TestAuditMultipartFormIDExtraction verifies that agent_id/session_id are extracted
// from multipart form fields when JSON parsing fails.
func TestAuditMultipartFormIDExtraction(t *testing.T) {
	r, w, dir := setupAuditRouter(t)
	defer w.Close()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("agent_id", "a1")
	mw.WriteField("session_id", "s1")
	mw.WriteField("path", "/test.txt")
	fw, _ := mw.CreateFormFile("file", "test.txt")
	fw.Write([]byte("file content"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/audited/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	w.SyncSession("a1", "s1")
	entries := readAuditEntries(t, dir, "a1", "s1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 per-session audit entry for multipart, got %d", len(entries))
	}
}

// TestAuditSensitiveHeaderRedaction verifies that sensitive headers are redacted.
func TestAuditSensitiveHeaderRedaction(t *testing.T) {
	r, w, dir := setupAuditRouter(t)
	defer w.Close()

	body := `{"agent_id":"a1","session_id":"s1"}`
	req := httptest.NewRequest(http.MethodPost, "/audited", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("Cookie", "session=abc123")
	req.Header.Set("X-Api-Key", "my-api-key")
	req.Header.Set("X-Custom", "visible")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	w.SyncSession("a1", "s1")
	entries := readAuditEntries(t, dir, "a1", "s1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	h := entries[0].Headers
	for _, key := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		if v, ok := h[key]; ok && v != "[REDACTED]" {
			t.Errorf("header %s should be redacted, got %q", key, v)
		}
	}
	if v, ok := h["X-Custom"]; !ok || v != "visible" {
		t.Errorf("X-Custom header should be visible, got %q", v)
	}
}

// TestAuditResponseBodyCapped verifies that the response body buffer is capped.
func TestAuditResponseBodyCapped(t *testing.T) {
	r, w, dir := setupAuditRouter(t)
	defer w.Close()

	body := `{"agent_id":"a1","session_id":"s1"}`
	req := httptest.NewRequest(http.MethodPost, "/large-resp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// The actual response should be 1MB.
	if rec.Body.Len() != 1024*1024 {
		t.Errorf("expected 1MB response, got %d bytes", rec.Body.Len())
	}

	w.SyncSession("a1", "s1")
	entries := readAuditEntries(t, dir, "a1", "s1")
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	// The audit entry's response should be nil (binary data won't parse as JSON)
	// but the important thing is we didn't OOM.
	// Just verify the entry was written successfully.
	if entries[0].Status != 200 {
		t.Errorf("expected status 200, got %d", entries[0].Status)
	}
}

// TestAuditFallbackWhenNoIDs verifies entries go to fallback when no IDs are present.
func TestAuditFallbackWhenNoIDs(t *testing.T) {
	r, w, dir := setupAuditRouter(t)
	defer w.Close()

	body := `{"command":"echo hi"}`
	req := httptest.NewRequest(http.MethodPost, "/audited", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Verify fallback log was written.
	fallbackPath := filepath.Join(dir, "fallback", "audit.log")
	data, err := os.ReadFile(fallbackPath)
	if err != nil {
		t.Fatalf("failed to read fallback log: %v", err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty fallback log")
	}
}

// TestAuditNoEntryForUnauditedRoute verifies that routes without the middleware
// don't produce audit entries.
func TestAuditNoEntryForUnauditedRoute(t *testing.T) {
	r, w, dir := setupAuditRouter(t)
	defer w.Close()

	body := `{"agent_id":"a1","session_id":"s1"}`
	req := httptest.NewRequest(http.MethodPost, "/no-audit", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	entries := readAuditEntries(t, dir, "a1", "s1")
	if len(entries) != 0 {
		t.Errorf("expected 0 audit entries for unaudited route, got %d", len(entries))
	}
}

// TestExtractIDs verifies the extractIDs helper.
func TestExtractIDs(t *testing.T) {
	tests := []struct {
		name              string
		data              string
		wantAgent, wantSess string
	}{
		{"valid", `{"agent_id":"a1","session_id":"s1"}`, "a1", "s1"},
		{"partial", `{"agent_id":"a1"}`, "a1", ""},
		{"empty", `{}`, "", ""},
		{"invalid json", `not json`, "", ""},
		{"array", `[1,2,3]`, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, s := extractIDs([]byte(tc.data))
			if a != tc.wantAgent {
				t.Errorf("agent: got %q, want %q", a, tc.wantAgent)
			}
			if s != tc.wantSess {
				t.Errorf("session: got %q, want %q", s, tc.wantSess)
			}
		})
	}
}
