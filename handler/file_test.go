package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func setupRouter() (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := tTempDir()
	mgr := session.NewManager(dir, 24*time.Hour)

	r := gin.New()

	fileH := NewFileHandler(mgr)
	f := r.Group("/v1/file")
	{
		f.POST("/read", fileH.Read)
		f.POST("/write", fileH.Write)
		f.POST("/replace", fileH.Replace)
		f.POST("/search", fileH.Search)
		f.POST("/find", fileH.Find)
		f.POST("/grep", fileH.Grep)
		f.POST("/glob", fileH.Glob)
		f.POST("/list", fileH.List)
		f.GET("/download", fileH.Download)
		f.POST("/upload", fileH.Upload)
	}

	return r, mgr
}

func tTempDir() string {
	dir := filepath.Join(os.TempDir(), "sandbox-test-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	os.MkdirAll(dir, 0755)
	return dir
}

func TestFileWriteAndRead(t *testing.T) {
	r, _ := setupRouter()

	// Write
	body := `{"agent_id": "a1", "session_id": "test1", "file": "/hello.txt", "content": "hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write failed: %d %s", w.Code, w.Body.String())
	}

	// Read
	body = `{"agent_id": "a1", "session_id": "test1", "file": "/hello.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("read failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "hello world" {
		t.Errorf("expected 'hello world', got %v", data["content"])
	}
}

func TestFileReadWithLines(t *testing.T) {
	r, _ := setupRouter()

	// Write multi-line file
	body := `{"agent_id": "a1", "session_id": "test2", "file": "/lines.txt", "content": "line1\nline2\nline3\nline4\nline5"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read lines 1-3 (0-based)
	body = `{"agent_id": "a1", "session_id": "test2", "file": "/lines.txt", "start_line": 1, "end_line": 3}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	content := data["content"].(string)
	if content != "line2\nline3" {
		t.Errorf("expected 'line2\\nline3', got %q", content)
	}
}

func TestFileReplace(t *testing.T) {
	r, _ := setupRouter()

	// Write
	body := `{"agent_id": "a1", "session_id": "test3", "file": "/replace.txt", "content": "foo bar foo"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Replace
	body = `{"agent_id": "a1", "session_id": "test3", "file": "/replace.txt", "old_str": "foo", "new_str": "baz"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if int(data["replaced_count"].(float64)) != 2 {
		t.Errorf("expected 2 replacements, got %v", data["replaced_count"])
	}

	// Verify
	body = `{"agent_id": "a1", "session_id": "test3", "file": "/replace.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &resp)
	data = resp["data"].(map[string]interface{})
	if data["content"] != "baz bar baz" {
		t.Errorf("expected 'baz bar baz', got %v", data["content"])
	}
}

func TestFileSearch(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test4", "file": "/search.txt", "content": "hello world\nfoo bar\nhello again"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body = `{"agent_id": "a1", "session_id": "test4", "file": "/search.txt", "regex": "hello"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	matches := data["matches"].([]interface{})
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

func TestFileList(t *testing.T) {
	r, _ := setupRouter()

	// Create files
	for _, f := range []string{"/a.txt", "/b.txt", "/sub/c.txt"} {
		body := fmt.Sprintf(`{"agent_id": "a1", "session_id": "test5", "file": "%s", "content": "data"}`, f)
		req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	// List
	body := `{"agent_id": "a1", "session_id": "test5", "path": "/"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) < 3 {
		t.Errorf("expected at least 3 items (a.txt, b.txt, sub/), got %d", len(files))
	}
	// Verify structure: should contain a.txt, b.txt, and sub directory
	names := make(map[string]bool)
	for _, f := range files {
		fi := f.(map[string]interface{})
		names[fi["name"].(string)] = true
	}
	if !names["a.txt"] || !names["b.txt"] || !names["sub"] {
		t.Errorf("expected a.txt, b.txt, sub in listing, got %v", names)
	}
}

func TestFileFind(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test6", "file": "/readme.md", "content": "# Hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body = `{"agent_id": "a1", "session_id": "test6", "path": "/", "glob": "*.md"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/find", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(files), files)
	}
}

func TestFileGrep(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test7", "file": "/grep_test.txt", "content": "hello world\nfoo bar\nhello again\nbaz"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body = `{"agent_id": "a1", "session_id": "test7", "path": "/", "pattern": "hello", "include": ["grep_test.txt"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/grep", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	matches := data["matches"].([]interface{})
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

func TestFileWriteAutoMkdir(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test8", "file": "/deep/nested/dir/file.txt", "content": "auto created"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write with auto mkdir failed: %d %s", w.Code, w.Body.String())
	}
}

func TestFileAppend(t *testing.T) {
	r, _ := setupRouter()

	// Initial write
	body := `{"agent_id": "a1", "session_id": "test9", "file": "/append.txt", "content": "line1"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Append
	body = `{"agent_id": "a1", "session_id": "test9", "file": "/append.txt", "content": "line2", "append": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read
	body = `{"agent_id": "a1", "session_id": "test9", "file": "/append.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "line1line2" {
		t.Errorf("expected 'line1line2', got %v", data["content"])
	}
}

func TestFileUpload(t *testing.T) {
	r, _ := setupRouter()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("agent_id", "a1")
	writer.WriteField("session_id", "test10")
	writer.WriteField("path", "/uploaded.txt")
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded content"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/file/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload failed: %d %s", w.Code, w.Body.String())
	}

	// Verify the uploaded content by reading it back
	readBody := `{"agent_id": "a1", "session_id": "test10", "file": "/uploaded.txt"}`
	readReq := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(readBody))
	readReq.Header.Set("Content-Type", "application/json")
	readW := httptest.NewRecorder()
	r.ServeHTTP(readW, readReq)

	if readW.Code != http.StatusOK {
		t.Fatalf("read after upload failed: %d %s", readW.Code, readW.Body.String())
	}
	var readResp map[string]interface{}
	json.Unmarshal(readW.Body.Bytes(), &readResp)
	readData := readResp["data"].(map[string]interface{})
	if readData["content"] != "uploaded content" {
		t.Errorf("expected uploaded content 'uploaded content', got %v", readData["content"])
	}
}

func TestFileDownload(t *testing.T) {
	r, _ := setupRouter()

	// Write first
	body := `{"agent_id": "a1", "session_id": "test11", "file": "/download.txt", "content": "download me"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Download
	req = httptest.NewRequest(http.MethodGet, "/v1/file/download?agent_id=a1&session_id=test11&path=/download.txt", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download failed: %d %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "download me" {
		t.Errorf("expected 'download me', got %s", w.Body.String())
	}
}

func TestSessionIsolation(t *testing.T) {
	r, _ := setupRouter()

	// Write to session A
	body := `{"agent_id": "a1", "session_id": "sessA", "file": "/secret.txt", "content": "secret A"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write to session B
	body = `{"agent_id": "a1", "session_id": "sessB", "file": "/secret.txt", "content": "secret B"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read session A
	body = `{"agent_id": "a1", "session_id": "sessA", "file": "/secret.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "secret A" {
		t.Errorf("session isolation broken: expected 'secret A', got %v", data["content"])
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test12", "file": "/../../../etc/passwd", "content": "hack"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("path traversal should be blocked, got %d", w.Code)
	}
}

func TestFileWriteBase64(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test13", "file": "/binary.bin", "content": "SGVsbG8gV29ybGQ=", "encoding": "base64"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("base64 write failed: %d %s", w.Code, w.Body.String())
	}

	// Read back
	body = `{"agent_id": "a1", "session_id": "test13", "file": "/binary.bin"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "Hello World" {
		t.Errorf("expected 'Hello World', got %v", data["content"])
	}
}

func TestSkillsPathReadOnly(t *testing.T) {
	r, mgr := setupRouter()

	// Create a file in skills directory
	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "test-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "test-skill", "SKILLS.MD"), []byte("---\nname: test\n---\ncontent"), 0644)

	// Write to skills path should fail
	body := `{"agent_id": "a1", "session_id": "test14", "file": "/skills/test-skill/new.txt", "content": "hack"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("write to skills should be forbidden, got %d", w.Code)
	}

	// Replace in skills path should fail
	body = `{"agent_id": "a1", "session_id": "test14", "file": "/skills/test-skill/SKILLS.MD", "old_str": "content", "new_str": "hacked"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("replace in skills should be forbidden, got %d", w.Code)
	}

	// Read from skills path should work
	body = `{"agent_id": "a1", "session_id": "test14", "file": "/skills/test-skill/SKILLS.MD"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("read from skills should succeed, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSkillsPathList(t *testing.T) {
	r, mgr := setupRouter()

	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "my-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "my-skill", "SKILLS.MD"), []byte("---\nname: my-skill\n---\ncontent"), 0644)

	body := `{"agent_id": "a1", "session_id": "test15", "path": "/skills"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list skills failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) < 1 {
		t.Errorf("expected at least 1 item in skills listing, got %d", len(files))
	}
}

func TestAgentIsolation(t *testing.T) {
	r, _ := setupRouter()

	// Write to agent a1
	body := `{"agent_id": "a1", "session_id": "sess1", "file": "/secret.txt", "content": "agent1 secret"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write to agent a2
	body = `{"agent_id": "a2", "session_id": "sess1", "file": "/secret.txt", "content": "agent2 secret"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read agent a1
	body = `{"agent_id": "a1", "session_id": "sess1", "file": "/secret.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "agent1 secret" {
		t.Errorf("agent isolation broken: expected 'agent1 secret', got %v", data["content"])
	}
}

func TestFileReadNotFound(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test20", "file": "/nonexistent.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent file, got %d", w.Code)
	}
}

func TestFileSearchInvalidRegex(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test21", "file": "/test.txt", "regex": "[invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid regex, got %d", w.Code)
	}
}

func TestFileReplaceNoMatch(t *testing.T) {
	r, _ := setupRouter()

	// Write a file
	body := `{"agent_id": "a1", "session_id": "test22", "file": "/nomatch.txt", "content": "hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Replace with non-matching old_str
	body = `{"agent_id": "a1", "session_id": "test22", "file": "/nomatch.txt", "old_str": "xyz", "new_str": "abc"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("replace no match: expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if int(data["replaced_count"].(float64)) != 0 {
		t.Errorf("expected 0 replacements, got %v", data["replaced_count"])
	}
}

func TestPathTraversalReadBlocked(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test23", "file": "/../../../etc/passwd"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("path traversal read should be blocked, got %d", w.Code)
	}
}

func TestFileWriteMissingRequired(t *testing.T) {
	r, _ := setupRouter()

	// Missing file field
	body := `{"agent_id": "a1", "session_id": "test24", "content": "data"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing file field, got %d", w.Code)
	}
}
