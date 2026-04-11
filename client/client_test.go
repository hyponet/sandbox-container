package client

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"sandbox-container/handler"
	"sandbox-container/session"

	"github.com/gin-gonic/gin"
)

// setupTestServer creates a full API server for integration testing.
func setupTestServer(t *testing.T) (*Client, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := filepath.Join(os.TempDir(), "client-test-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	os.MkdirAll(dir, 0755)
	mgr := session.NewManager(dir, 24*time.Hour)

	r := gin.New()
	r.Use(gin.Recovery())

	// Sandbox
	sandboxH := handler.NewSandboxHandler()
	r.GET("/v1/sandbox", sandboxH.GetContext)

	// Bash
	bashH := handler.NewBashHandler(mgr)
	bash := r.Group("/v1/bash")
	{
		bash.POST("/exec", bashH.Exec)
		bash.POST("/output", bashH.Output)
		bash.POST("/write", bashH.Write)
		bash.POST("/kill", bashH.Kill)
		bash.GET("/sessions", bashH.ListSessions)
		bash.POST("/sessions/create", bashH.CreateSession)
		bash.POST("/sessions/:session_id/close", bashH.CloseSession)
	}

	// File
	fileH := handler.NewFileHandler(mgr)
	f := r.Group("/v1/file")
	{
		f.POST("/read", fileH.Read)
		f.POST("/write", fileH.Write)
		f.POST("/replace", fileH.Replace)
		f.POST("/search", fileH.Search)
		f.POST("/find", fileH.Find)
		f.POST("/grep", fileH.Grep)
		f.POST("/glob", fileH.Glob)
		f.POST("/upload", fileH.Upload)
		f.GET("/download", fileH.Download)
		f.POST("/list", fileH.List)
	}

	// Code
	codeH := handler.NewCodeHandler(mgr)
	r.POST("/v1/code/execute", codeH.Execute)
	r.GET("/v1/code/info", codeH.Info)

	// Skills
	skillH := handler.NewSkillHandler(mgr)
	skills := r.Group("/v1/skills")
	{
		skills.POST("/list", skillH.List)
		skills.POST("/load", skillH.Load)
	}

	server := httptest.NewServer(r)
	cli := NewClient(server.URL)

	cleanup := func() {
		server.Close()
		os.RemoveAll(dir)
	}

	return cli, cleanup
}

// =============================================
// Sandbox tests
// =============================================

func TestGetSandboxContext(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	ctx, err := cli.GetSandboxContext()
	if err != nil {
		t.Fatalf("GetSandboxContext failed: %v", err)
	}
	if ctx.Version == "" {
		t.Error("expected non-empty version")
	}
	if ctx.Detail.System.OS == "" {
		t.Error("expected non-empty OS")
	}
}

// =============================================
// Bash tests
// =============================================

func TestBashExec(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.BashExec("a1", "s1", "echo hello")
	if err != nil {
		t.Fatalf("BashExec failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "hello\n" {
		t.Errorf("expected stdout 'hello\\n', got %v", result.Stdout)
	}
}

func TestBashExecExitCode(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.BashExec("a1", "s2", "exit 42")
	if err != nil {
		t.Fatalf("BashExec failed: %v", err)
	}
	if result.ExitCode == nil || *result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %v", result.ExitCode)
	}
}

func TestBashExecEnv(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.BashExec("a1", "s3", "echo $MY_VAR", WithEnv(map[string]string{"MY_VAR": "hello"}))
	if err != nil {
		t.Fatalf("BashExec failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "hello\n" {
		t.Errorf("expected stdout 'hello\\n', got %v", result.Stdout)
	}
}

func TestBashExecAsync(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.BashExec("a1", "s4", "sleep 0.1 && echo done", WithAsyncMode(true))
	if err != nil {
		t.Fatalf("BashExec async failed: %v", err)
	}
	if result.Status != StatusRunning {
		t.Errorf("expected status running, got %s", result.Status)
	}

	// Poll for output until command completes
	deadline := time.Now().Add(3 * time.Second)
	var out *BashOutputResult
	for time.Now().Before(deadline) {
		out, err = cli.BashOutput("a1", "s4", result.CommandID, 0, 0)
		if err != nil {
			t.Fatalf("BashOutput failed: %v", err)
		}
		if out.Command != nil && out.Command.Status == StatusCompleted {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if out == nil {
		t.Fatal("never got output")
	}
	if out.Stdout != "done\n" {
		t.Errorf("expected stdout 'done\\n', got %q", out.Stdout)
	}
}

func TestBashCreateSession(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	info, err := cli.BashCreateSession("a1", "s5")
	if err != nil {
		t.Fatalf("BashCreateSession failed: %v", err)
	}
	// BashSessionID defaults to "default" when not explicitly set
	if info.SessionID != "default" {
		t.Errorf("expected session_id 'default', got %s", info.SessionID)
	}
	if info.Status != SessionReady {
		t.Errorf("expected status ready, got %s", info.Status)
	}
}

func TestBashListSessions(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.BashCreateSession("a1", "s6")
	if err != nil {
		t.Fatalf("BashCreateSession failed: %v", err)
	}

	sessions, err := cli.BashListSessions("s6")
	if err != nil {
		t.Fatalf("BashListSessions failed: %v", err)
	}
	if len(sessions) < 1 {
		t.Errorf("expected at least 1 session, got %d", len(sessions))
	}
}

func TestBashExecMultiLine(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.BashExec("a1", "s7", "echo line1 && echo line2")
	if err != nil {
		t.Fatalf("BashExec failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "line1\nline2\n" {
		t.Errorf("expected multi-line output, got %v", result.Stdout)
	}
}

// =============================================
// File tests
// =============================================

func TestFileWriteAndRead(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "s1", "/hello.txt", "hello world")
	if err != nil {
		t.Fatalf("FileWrite failed: %v", err)
	}

	result, err := cli.FileRead("a1", "s1", "/hello.txt")
	if err != nil {
		t.Fatalf("FileRead failed: %v", err)
	}
	if result.Content != "hello world" {
		t.Errorf("expected 'hello world', got %q", result.Content)
	}
}

func TestFileReadWithLines(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "s2", "/lines.txt", "line1\nline2\nline3\nline4\nline5")
	if err != nil {
		t.Fatalf("FileWrite failed: %v", err)
	}

	result, err := cli.FileRead("a1", "s2", "/lines.txt", WithLineRange(1, 3))
	if err != nil {
		t.Fatalf("FileRead with lines failed: %v", err)
	}
	if result.Content != "line2\nline3" {
		t.Errorf("expected 'line2\\nline3', got %q", result.Content)
	}
}

func TestFileReplace(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "s3", "/replace.txt", "foo bar foo")
	if err != nil {
		t.Fatalf("FileWrite failed: %v", err)
	}

	rr, err := cli.FileReplace("a1", "s3", "/replace.txt", "foo", "baz")
	if err != nil {
		t.Fatalf("FileReplace failed: %v", err)
	}
	if rr.ReplacedCount != 2 {
		t.Errorf("expected 2 replacements, got %d", rr.ReplacedCount)
	}

	result, _ := cli.FileRead("a1", "s3", "/replace.txt")
	if result.Content != "baz bar baz" {
		t.Errorf("expected 'baz bar baz', got %q", result.Content)
	}
}

func TestFileSearch(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "s4", "/search.txt", "hello world\nfoo bar\nhello again")

	result, err := cli.FileSearch("a1", "s4", "/search.txt", "hello")
	if err != nil {
		t.Fatalf("FileSearch failed: %v", err)
	}
	if len(result.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(result.Matches))
	}
}

func TestFileFind(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "s5", "/readme.md", "# Hello")

	result, err := cli.FileFind("a1", "s5", "/", "*.md")
	if err != nil {
		t.Fatalf("FileFind failed: %v", err)
	}
	if len(result.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(result.Files))
	}
}

func TestFileGrep(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "s6", "/grep_test.txt", "hello world\nfoo bar\nhello again\nbaz")

	result, err := cli.FileGrep("a1", "s6", "/", "hello", WithInclude([]string{"grep_test.txt"}))
	if err != nil {
		t.Fatalf("FileGrep failed: %v", err)
	}
	if len(result.Matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(result.Matches))
	}
}

func TestFileGlob(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "s7", "/test.txt", "data")
	cli.FileWrite("a1", "s7", "/test.go", "package main")

	result, err := cli.FileGlob("a1", "s7", "/", "*.go")
	if err != nil {
		t.Fatalf("FileGlob failed: %v", err)
	}
	if len(result.Files) != 1 {
		t.Errorf("expected 1 file, got %d", len(result.Files))
	}
}

func TestFileList(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "s8", "/a.txt", "a")
	cli.FileWrite("a1", "s8", "/b.txt", "b")

	result, err := cli.FileList("a1", "s8", "/")
	if err != nil {
		t.Fatalf("FileList failed: %v", err)
	}
	if len(result.Files) < 2 {
		t.Errorf("expected at least 2 files, got %d", len(result.Files))
	}
}

func TestFileUpload(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	content := bytes.NewReader([]byte("uploaded content"))
	result, err := cli.FileUpload("a1", "s9", "/uploaded.txt", content, "test.txt")
	if err != nil {
		t.Fatalf("FileUpload failed: %v", err)
	}
	if !result.Success {
		t.Error("expected success")
	}

	// Verify content
	readResult, err := cli.FileRead("a1", "s9", "/uploaded.txt")
	if err != nil {
		t.Fatalf("FileRead after upload failed: %v", err)
	}
	if readResult.Content != "uploaded content" {
		t.Errorf("expected 'uploaded content', got %q", readResult.Content)
	}
}

func TestFileDownload(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "s10", "/download.txt", "download me")

	data, err := cli.FileDownload("a1", "s10", "/download.txt")
	if err != nil {
		t.Fatalf("FileDownload failed: %v", err)
	}
	if string(data) != "download me" {
		t.Errorf("expected 'download me', got %q", string(data))
	}
}

func TestFileAppend(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "s11", "/append.txt", "line1")
	cli.FileWrite("a1", "s11", "/append.txt", "line2", WithAppend(true))

	result, err := cli.FileRead("a1", "s11", "/append.txt")
	if err != nil {
		t.Fatalf("FileRead failed: %v", err)
	}
	if result.Content != "line1line2" {
		t.Errorf("expected 'line1line2', got %q", result.Content)
	}
}

func TestFileWriteBase64(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "s12", "/binary.bin", "SGVsbG8gV29ybGQ=", WithEncoding("base64"))
	if err != nil {
		t.Fatalf("FileWrite base64 failed: %v", err)
	}

	result, err := cli.FileRead("a1", "s12", "/binary.bin")
	if err != nil {
		t.Fatalf("FileRead failed: %v", err)
	}
	if result.Content != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", result.Content)
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "s13", "/../../../etc/passwd", "hack")
	if err == nil {
		t.Error("expected path traversal to be blocked")
	}
	if apiErr, ok := err.(*Error); !ok || apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 error, got %v", err)
	}
}

func TestFileWriteAutoMkdir(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "s14", "/deep/nested/dir/file.txt", "auto created")
	if err != nil {
		t.Fatalf("FileWrite auto mkdir failed: %v", err)
	}
}

func TestSkillsPathReadOnly(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// First create a skill via skill list
	zipURL := createTestZIPServer(t, map[string]string{
		"SKILLS.MD": "---\nname: test\n---\ncontent",
	})

	_, err := cli.SkillList("a1", []string{zipURL + "?slug=test"})
	if err != nil {
		t.Fatalf("SkillList failed: %v", err)
	}

	// Write to skills path should fail
	_, err = cli.FileWrite("a1", "s15", "/skills/test/new.txt", "hack")
	if err == nil {
		t.Error("expected write to skills to be blocked")
	}
	if apiErr, ok := err.(*Error); !ok || apiErr.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 error, got %v", err)
	}

	// Read from skills path should work
	_, err = cli.FileRead("a1", "s15", "/skills/test/SKILLS.MD")
	if err != nil {
		t.Errorf("expected read from skills to succeed, got %v", err)
	}
}

// =============================================
// Code tests
// =============================================

func TestCodeExecutePython(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.CodeExecute("a1", "s1", "python", "print(2+2)")
	if err != nil {
		t.Fatalf("CodeExecute python failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "4\n" {
		t.Errorf("expected stdout '4\\n', got %v", result.Stdout)
	}
}

func TestCodeExecuteJavaScript(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.CodeExecute("a1", "s2", "javascript", "console.log(2+2)")
	if err != nil {
		t.Fatalf("CodeExecute javascript failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "4\n" {
		t.Errorf("expected stdout '4\\n', got %v", result.Stdout)
	}
}

func TestCodeInfo(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	info, err := cli.CodeInfo()
	if err != nil {
		t.Fatalf("CodeInfo failed: %v", err)
	}
	if len(info.Languages) < 2 {
		t.Errorf("expected at least 2 languages, got %d", len(info.Languages))
	}
}

func TestCodeExecuteUnsupportedLang(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.CodeExecute("a1", "s3", "rust", "fn main() {}")
	if err == nil {
		t.Error("expected error for unsupported language")
	}
}

// =============================================
// Skill tests
// =============================================

func TestSkillList(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	zipURL := createTestZIPServer(t, map[string]string{
		"SKILLS.MD": "---\nname: test-skill\ndescription: A test skill\ntype: prompt\n---\nThis is the skill content.",
		"script.sh": "echo hello",
	})

	result, err := cli.SkillList("a1", []string{zipURL + "?slug=my-skill"})
	if err != nil {
		t.Fatalf("SkillList failed: %v", err)
	}
	if len(result.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result.Skills))
	}
	if result.Skills[0].Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %s", result.Skills[0].Name)
	}
	if result.Skills[0].Description != "A test skill" {
		t.Errorf("expected description 'A test skill', got %s", result.Skills[0].Description)
	}
}

func TestSkillLoad(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	zipURL := createTestZIPServer(t, map[string]string{
		"SKILLS.MD": "---\nname: load-skill\n---\nSkill content here.",
	})

	// First list to download and extract
	_, err := cli.SkillList("a1", []string{zipURL + "?slug=load-skill"})
	if err != nil {
		t.Fatalf("SkillList failed: %v", err)
	}

	// Then load
	result, err := cli.SkillLoad("a1", []string{"load-skill"})
	if err != nil {
		t.Fatalf("SkillLoad failed: %v", err)
	}
	if len(result.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result.Skills))
	}
	if result.Skills[0].Name != "load-skill" {
		t.Errorf("expected name 'load-skill', got %s", result.Skills[0].Name)
	}
}

func TestSkillLoadNotFound(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillLoad("a1", []string{"nonexistent"})
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
	if apiErr, ok := err.(*Error); !ok || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 error, got %v", err)
	}
}

// =============================================
// Agent isolation test
// =============================================

func TestAgentIsolation(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("agent1", "s1", "/secret.txt", "agent1 secret")
	cli.FileWrite("agent2", "s1", "/secret.txt", "agent2 secret")

	result, err := cli.FileRead("agent1", "s1", "/secret.txt")
	if err != nil {
		t.Fatalf("FileRead failed: %v", err)
	}
	if result.Content != "agent1 secret" {
		t.Errorf("agent isolation broken: expected 'agent1 secret', got %q", result.Content)
	}
}

// =============================================
// Helpers
// =============================================

// createTestZIPServer creates a test ZIP file and serves it via httptest.Server.
// Returns the server URL.
func createTestZIPServer(t *testing.T, files map[string]string) string {
	t.Helper()

	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(f, content)
	}
	w.Close()

	zipData := buf.Bytes()

	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server.URL + "/skill.zip"
}
