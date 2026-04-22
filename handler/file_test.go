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

	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func setupRouter() (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := tTempDir()
	mgr := session.NewManager(dir, 24*time.Hour)

	r := gin.New()

	fileH := NewFileHandler(mgr, &executor.DirectFileOperator{})
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

func TestFileGlobRecursiveRespectsHiddenFlag(t *testing.T) {
	r, _ := setupRouter()

	for _, f := range []string{"/root.go", "/nested/code.go", "/nested/.hidden.go", "/.hidden-root.go"} {
		body := fmt.Sprintf(`{"agent_id": "a1", "session_id": "glob_recursive", "file": "%s", "content": "package main"}`, f)
		req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("write %s failed: %d %s", f, w.Code, w.Body.String())
		}
	}

	body := `{"agent_id": "a1", "session_id": "glob_recursive", "path": "/", "pattern": "**/*.go"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/glob", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("glob failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) != 2 {
		t.Fatalf("expected 2 visible matches, got %d: %v", len(files), files)
	}

	names := map[string]bool{}
	for _, f := range files {
		fi := f.(map[string]interface{})
		names[fi["path"].(string)] = true
	}
	if !names["/root.go"] || !names["/nested/code.go"] {
		t.Fatalf("unexpected glob paths: %v", names)
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

func TestFileWrite_AgentWorkspace(t *testing.T) {
	r, mgr := setupRouter()

	// Write with enable_agent_workspace=true
	body := `{"agent_id": "a1", "session_id": "test_dsi", "file": "/workspace-file.txt", "content": "in workspace", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	// Verify the file exists in the workspace directory, not the session directory
	wsRoot := mgr.WorkspaceRoot("a1")
	data, err := os.ReadFile(filepath.Join(wsRoot, "workspace-file.txt"))
	if err != nil {
		t.Fatalf("file not found in workspace dir: %v", err)
	}
	if string(data) != "in workspace" {
		t.Errorf("expected 'in workspace', got %q", string(data))
	}

	// Verify the file does NOT exist in the session directory
	sessionRoot := mgr.SessionRoot("a1", "test_dsi")
	_, err = os.Stat(filepath.Join(sessionRoot, "workspace-file.txt"))
	if err == nil {
		t.Error("file should NOT exist in session directory when enable_agent_workspace is true")
	}
}

func TestFileRead_AgentWorkspace(t *testing.T) {
	r, mgr := setupRouter()

	// Pre-create a file in the workspace directory
	wsRoot := mgr.WorkspaceRoot("a1")
	os.MkdirAll(wsRoot, 0755)
	os.WriteFile(filepath.Join(wsRoot, "ws-read-test.txt"), []byte("workspace content"), 0644)

	// Read with enable_agent_workspace=true
	body := `{"agent_id": "a1", "session_id": "test_dsi_read", "file": "/ws-read-test.txt", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("read with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "workspace content" {
		t.Errorf("expected 'workspace content', got %v", data["content"])
	}
}

func TestFileWrite_AgentWorkspace_Skills(t *testing.T) {
	r, mgr := setupRouter()

	// Pre-create the skills directory with a skill
	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "my-skill"), 0755)

	// Write to skills path with enable_agent_workspace=true
	body := `{"agent_id": "a1", "session_id": "test_sw", "file": "/skills/my-skill/new-file.txt", "content": "skill data", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write to skills with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	// Verify the file was created in the skills directory
	data, err := os.ReadFile(filepath.Join(skillsDir, "my-skill", "new-file.txt"))
	if err != nil {
		t.Fatalf("file not found in skills dir: %v", err)
	}
	if string(data) != "skill data" {
		t.Errorf("expected 'skill data', got %q", string(data))
	}
}

func TestFileReplace_AgentWorkspace_Skills(t *testing.T) {
	r, mgr := setupRouter()

	// Pre-create a skill file in the skills directory
	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "replace-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "replace-skill", "config.txt"), []byte("foo bar foo"), 0644)

	// Replace in skills path with enable_agent_workspace=true
	body := `{"agent_id": "a1", "session_id": "test_sw_replace", "file": "/skills/replace-skill/config.txt", "old_str": "foo", "new_str": "baz", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("replace in skills with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if int(data["replaced_count"].(float64)) != 2 {
		t.Errorf("expected 2 replacements, got %v", data["replaced_count"])
	}

	// Verify file content on disk
	content, err := os.ReadFile(filepath.Join(skillsDir, "replace-skill", "config.txt"))
	if err != nil {
		t.Fatalf("failed to read skills file: %v", err)
	}
	if string(content) != "baz bar baz" {
		t.Errorf("expected 'baz bar baz', got %q", string(content))
	}
}

func TestFileUpload_AgentWorkspace(t *testing.T) {
	r, mgr := setupRouter()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("agent_id", "a1")
	writer.WriteField("session_id", "test_dsi_upload")
	writer.WriteField("path", "/upload-ws.txt")
	writer.WriteField("enable_agent_workspace", "true")
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded in workspace mode"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/file/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	// Verify the file landed in the workspace directory
	wsRoot := mgr.WorkspaceRoot("a1")
	data, err := os.ReadFile(filepath.Join(wsRoot, "upload-ws.txt"))
	if err != nil {
		t.Fatalf("file not found in workspace dir: %v", err)
	}
	if string(data) != "uploaded in workspace mode" {
		t.Errorf("expected 'uploaded in workspace mode', got %q", string(data))
	}
}

func TestFileUpload_AgentWorkspace_Skills(t *testing.T) {
	r, mgr := setupRouter()

	// Pre-create skills directory
	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "upload-skill"), 0755)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("agent_id", "a1")
	writer.WriteField("session_id", "test_sw_upload")
	writer.WriteField("path", "/skills/upload-skill/uploaded.txt")
	writer.WriteField("enable_agent_workspace", "true")
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded to skills"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/file/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload to skills with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	// Verify the file was created in the skills directory
	data, err := os.ReadFile(filepath.Join(skillsDir, "upload-skill", "uploaded.txt"))
	if err != nil {
		t.Fatalf("file not found in skills dir: %v", err)
	}
	if string(data) != "uploaded to skills" {
		t.Errorf("expected 'uploaded to skills', got %q", string(data))
	}
}

// TestFileWrite_AgentWorkspace_SkillsAndWorkspace verifies that enable_agent_workspace=true
// enables both skills writing and workspace-mode path resolution in a single flag.
func TestFileWrite_AgentWorkspace_SkillsAndWorkspace(t *testing.T) {
	r, mgr := setupRouter()

	// Pre-create skills directory
	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "both-skill"), 0755)

	// Write to skills path with enable_agent_workspace — should allow skills write
	body := `{"agent_id": "a1", "session_id": "test_both", "file": "/skills/both-skill/combined.txt", "content": "both flags", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write skills with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	data, err := os.ReadFile(filepath.Join(skillsDir, "both-skill", "combined.txt"))
	if err != nil {
		t.Fatalf("file not found in skills dir: %v", err)
	}
	if string(data) != "both flags" {
		t.Errorf("expected 'both flags', got %q", string(data))
	}

	// Write to a non-skills path — should resolve to workspace dir, not session dir
	body = `{"agent_id": "a1", "session_id": "test_both", "file": "/workspace-both.txt", "content": "ws with both", "enable_agent_workspace": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write workspace file with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	wsRoot := mgr.WorkspaceRoot("a1")
	data, err = os.ReadFile(filepath.Join(wsRoot, "workspace-both.txt"))
	if err != nil {
		t.Fatalf("file not found in workspace dir: %v", err)
	}
	if string(data) != "ws with both" {
		t.Errorf("expected 'ws with both', got %q", string(data))
	}
}

// TestFileDownload_AgentWorkspace verifies download from workspace dir with enable_agent_workspace.
func TestFileDownload_AgentWorkspace(t *testing.T) {
	r, mgr := setupRouter()

	// Pre-create a file in the workspace directory
	wsRoot := mgr.WorkspaceRoot("a1")
	os.MkdirAll(wsRoot, 0755)
	os.WriteFile(filepath.Join(wsRoot, "dl-test.txt"), []byte("download me"), 0644)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/file/download?agent_id=a1&session_id=dl_ws&path=/dl-test.txt&enable_agent_workspace=true", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "download me" {
		t.Errorf("expected 'download me', got %q", w.Body.String())
	}
}

// TestFileWrite_SkillsReadOnly_Default verifies skills are read-only when enable_agent_workspace is false.
func TestFileWrite_SkillsReadOnly_Default(t *testing.T) {
	r, mgr := setupRouter()

	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "ro-skill"), 0755)

	// Write to skills path WITHOUT enable_agent_workspace — should be blocked
	body := `{"agent_id": "a1", "session_id": "test_ro", "file": "/skills/ro-skill/blocked.txt", "content": "nope"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for skills write without enable_agent_workspace, got %d", w.Code)
	}
}

func TestFileWrite_SkillsAliasReadOnly_Default(t *testing.T) {
	r, mgr := setupRouter()

	mgr.Touch("a1", "alias_ro")
	sessionRoot := mgr.SessionRoot("a1", "alias_ro")
	skillsDir := mgr.SkillsRoot("a1")
	if err := os.MkdirAll(filepath.Join(skillsDir, "aliased-skill"), 0755); err != nil {
		t.Fatalf("MkdirAll skills dir: %v", err)
	}
	if err := os.Symlink("skills/aliased-skill", filepath.Join(sessionRoot, "alias")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	body := `{"agent_id": "a1", "session_id": "alias_ro", "file": "/alias/blocked.txt", "content": "nope"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for aliased skills write, got %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "aliased-skill", "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected no file created via skills alias, stat err=%v", err)
	}
}

func TestFileOpOpts_SkillsReadOnlyOutsideWorkspace(t *testing.T) {
	_, mgr := setupRouter()
	h := NewFileHandler(mgr, &executor.DirectFileOperator{})

	sessionOpts := h.fileOpOpts("a1", "s1", false)
	if len(sessionOpts.RWBinds) != 1 || sessionOpts.RWBinds[0] != mgr.SessionRoot("a1", "s1") {
		t.Fatalf("session RWBinds = %v", sessionOpts.RWBinds)
	}
	if len(sessionOpts.ROBinds) != 1 || sessionOpts.ROBinds[0] != mgr.SkillsRoot("a1") {
		t.Fatalf("session ROBinds = %v", sessionOpts.ROBinds)
	}

	workspaceOpts := h.fileOpOpts("a1", "s1", true)
	if len(workspaceOpts.RWBinds) != 2 {
		t.Fatalf("workspace RWBinds = %v", workspaceOpts.RWBinds)
	}
	if workspaceOpts.RWBinds[0] != mgr.WorkspaceRoot("a1") || workspaceOpts.RWBinds[1] != mgr.SkillsRoot("a1") {
		t.Fatalf("workspace RWBinds = %v", workspaceOpts.RWBinds)
	}
	if len(workspaceOpts.ROBinds) != 0 {
		t.Fatalf("workspace ROBinds = %v", workspaceOpts.ROBinds)
	}
}
