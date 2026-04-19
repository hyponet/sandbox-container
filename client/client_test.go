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

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/handler"
	"github.com/hyponet/sandbox-container/middleware"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

// setupTestServer creates a full API server for integration testing.
func setupTestServer(t *testing.T) (*Client, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dir := filepath.Join(os.TempDir(), "client-test-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	os.MkdirAll(dir, 0755)
	globalSkillsDir := filepath.Join(dir, "global-skills")
	os.MkdirAll(globalSkillsDir, 0755)
	fallbackDir := filepath.Join(dir, "fallback-logs")
	mgr := session.NewManager(dir, 24*time.Hour)
	mgr.SetGlobalSkillsRoot(globalSkillsDir)

	auditW := audit.NewWriterWithFallback(dir, 5*time.Minute, fallbackDir)
	mgr.SetAuditWriter(auditW)
	auditMW := middleware.AuditLogger(auditW)

	r := gin.New()
	r.Use(gin.Recovery())

	// Sandbox
	sandboxH := handler.NewSandboxHandler()
	r.GET("/v1/sandbox", sandboxH.GetContext)
	r.GET("/v1/sandbox/packages/python", sandboxH.GetPythonPackages)
	r.GET("/v1/sandbox/packages/nodejs", sandboxH.GetNodejsPackages)

	// Bash
	bashH := handler.NewBashHandler(mgr)
	bash := r.Group("/v1/bash")
	{
		bash.POST("/exec", auditMW, bashH.Exec)
		bash.POST("/output", bashH.Output)
		bash.POST("/write", auditMW, bashH.Write)
		bash.POST("/kill", auditMW, bashH.Kill)
		bash.GET("/sessions", bashH.ListSessions)
		bash.POST("/sessions/create", auditMW, bashH.CreateSession)
		bash.POST("/sessions/:session_id/close", auditMW, bashH.CloseSession)
	}

	// File
	fileH := handler.NewFileHandler(mgr)
	f := r.Group("/v1/file")
	{
		f.POST("/read", fileH.Read)
		f.POST("/write", auditMW, fileH.Write)
		f.POST("/replace", auditMW, fileH.Replace)
		f.POST("/search", fileH.Search)
		f.POST("/find", fileH.Find)
		f.POST("/grep", fileH.Grep)
		f.POST("/glob", fileH.Glob)
		f.POST("/upload", auditMW, fileH.Upload)
		f.GET("/download", fileH.Download)
		f.POST("/list", fileH.List)
	}

	// Code
	codeH := handler.NewCodeHandler(mgr)
	r.POST("/v1/code/execute", auditMW, codeH.Execute)
	r.GET("/v1/code/info", codeH.Info)

	// Skills
	skillH := handler.NewSkillHandler(mgr)
	skillH.SetSSRFProtection(false) // disable for tests using httptest (loopback)
	skills := r.Group("/v1/skills", auditMW)
	{
		skills.POST("/create", skillH.Create)
		skills.POST("/get", skillH.Get)
		skills.POST("/update", skillH.Update)
		skills.POST("/rename", skillH.Rename)
		skills.POST("/import", skillH.Import)
		skills.POST("/list", skillH.ListGlobal)
		skills.POST("/delete", skillH.Delete)
		skills.POST("/tree", skillH.Tree)
		skills.POST("/copy", skillH.Copy)
		skills.GET("/export", skillH.Export)
		skills.POST("/file/read", skillH.FileRead)
		skills.POST("/file/write", skillH.FileWrite)
		skills.POST("/file/update", skillH.FileUpdate)
		skills.POST("/file/mkdir", skillH.FileMkdir)
		skills.POST("/file/delete", skillH.FileDelete)
		skills.POST("/import/upload", skillH.ImportUpload)
	}

	agents := r.Group("/v1/skills/agents", auditMW)
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
		agents.DELETE("/:agent_id/cache", skillH.AgentCacheDelete)
	}

	// Session Management
	sessionH := handler.NewSessionHandler(mgr)
	sess := r.Group("/v1/sessions")
	{
		sess.GET("", sessionH.ListSessions)
		sess.GET("/:session_id/audits", sessionH.GetAuditLogs)
		sess.DELETE("/:session_id", sessionH.DeleteSession)
	}

	server := httptest.NewServer(r)
	cli := NewClient(server.URL)

	cleanup := func() {
		auditW.Close()
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

	// Create a skill in global store
	_, err := cli.SkillCreate("test", "test skill")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	// Load it into agent's local cache
	_, err = cli.SkillAgentLoad("a1", []string{"test"})
	if err != nil {
		t.Fatalf("SkillAgentLoad failed: %v", err)
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
	_, err = cli.FileRead("a1", "s15", "/skills/test/SKILLS.md")
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

func TestSkillCreateAndList(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Create two skills
	_, err := cli.SkillCreate("skill-a", "First skill")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}
	_, err = cli.SkillCreate("skill-b", "Second skill")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	// List all skills
	result, err := cli.SkillList()
	if err != nil {
		t.Fatalf("SkillList failed: %v", err)
	}
	if len(result.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(result.Skills))
	}
}

func TestSkillImportAndLoad(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	zipURL := createTestZIPServer(t, map[string]string{
		"SKILLS.MD": "---\nname: imported-skill\ndescription: A test skill\n---\nThis is the skill content.",
		"script.sh": "echo hello",
	})

	// Import from ZIP
	_, err := cli.SkillImport("imported-skill", zipURL)
	if err != nil {
		t.Fatalf("SkillImport failed: %v", err)
	}

	// Load into agent
	loadResult, err := cli.SkillAgentLoad("a1", []string{"imported-skill"})
	if err != nil {
		t.Fatalf("SkillAgentLoad failed: %v", err)
	}
	if len(loadResult.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loadResult.Skills))
	}
	if loadResult.Skills[0].Name != "imported-skill" {
		t.Errorf("expected name 'imported-skill', got %s", loadResult.Skills[0].Name)
	}
}

func TestSkillFileOperations(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Create skill
	_, err := cli.SkillCreate("file-test", "test file ops")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	// Write a file
	writeResult, err := cli.SkillFileWrite("file-test", "test.txt", "hello world")
	if err != nil {
		t.Fatalf("SkillFileWrite failed: %v", err)
	}
	if writeResult.BytesWritten == 0 {
		t.Error("expected non-zero bytes written")
	}

	// Read the file
	readResult, err := cli.SkillFileRead("file-test", "test.txt")
	if err != nil {
		t.Fatalf("SkillFileRead failed: %v", err)
	}
	if readResult.Content != "hello world" {
		t.Errorf("expected 'hello world', got %q", readResult.Content)
	}

	// Update (replace)
	updateResult, err := cli.SkillFileUpdate("file-test", "test.txt", "hello", "goodbye")
	if err != nil {
		t.Fatalf("SkillFileUpdate failed: %v", err)
	}
	if updateResult.ReplacedCount != 1 {
		t.Errorf("expected 1 replacement, got %d", updateResult.ReplacedCount)
	}

	// Verify content after update
	readResult2, _ := cli.SkillFileRead("file-test", "test.txt")
	if readResult2.Content != "goodbye world" {
		t.Errorf("expected 'goodbye world', got %q", readResult2.Content)
	}

	// Create a directory
	mkdirResult, err := cli.SkillFileMkdir("file-test", "subdir")
	if err != nil {
		t.Fatalf("SkillFileMkdir failed: %v", err)
	}
	if mkdirResult.Path != "subdir" {
		t.Errorf("expected path 'subdir', got %q", mkdirResult.Path)
	}

	// Get tree
	treeResult, err := cli.SkillTree("file-test")
	if err != nil {
		t.Fatalf("SkillTree failed: %v", err)
	}
	if len(treeResult.Files) == 0 {
		t.Error("expected non-empty tree")
	}
}

func TestSkillAgentList(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillCreate("list-test", "A list test skill")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	result, err := cli.SkillAgentList("a1", []string{"list-test"})
	if err != nil {
		t.Fatalf("SkillAgentList failed: %v", err)
	}
	if len(result.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result.Skills))
	}
	s := result.Skills[0]
	if s.Name != "list-test" {
		t.Errorf("expected name 'list-test', got %s", s.Name)
	}
	if s.Path != "/skills/list-test" {
		t.Errorf("expected path '/skills/list-test', got %s", s.Path)
	}
	if s.Frontmatter == "" {
		t.Error("expected non-empty frontmatter")
	}
}

func TestSkillAgentListNotFound(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Handler skips nonexistent skills and returns 200 with empty results
	result, err := cli.SkillAgentList("a1", []string{"nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Skills) != 0 {
		t.Errorf("expected 0 skills for nonexistent, got %d", len(result.Skills))
	}
}

func TestSkillAgentLoadBody(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	zipURL := createTestZIPServer(t, map[string]string{
		"SKILLS.MD": "---\nname: body-test\ndescription: test\n---\n## Usage\nDo stuff.",
	})

	_, err := cli.SkillImport("body-test", zipURL)
	if err != nil {
		t.Fatalf("SkillImport failed: %v", err)
	}

	result, err := cli.SkillAgentLoad("a1", []string{"body-test"})
	if err != nil {
		t.Fatalf("SkillAgentLoad failed: %v", err)
	}
	if len(result.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(result.Skills))
	}
	content := result.Skills[0].Content
	if content != "## Usage\nDo stuff." {
		t.Errorf("expected body without frontmatter, got %q", content)
	}
}

func TestSkillAgentLoadNotFound(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Handler skips nonexistent skills and returns 200 with empty results
	result, err := cli.SkillAgentLoad("a1", []string{"nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Skills) != 0 {
		t.Errorf("expected 0 skills for nonexistent, got %d", len(result.Skills))
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
// Sandbox packages tests
// =============================================

func TestGetPythonPackages(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.GetPythonPackages()
	if err != nil {
		t.Fatalf("GetPythonPackages failed: %v", err)
	}
	// Result should be a non-nil slice (may be empty if pip3 is not installed)
	if result == nil {
		t.Error("expected non-nil result slice")
	}
}

func TestGetNodejsPackages(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.GetNodejsPackages()
	if err != nil {
		t.Fatalf("GetNodejsPackages failed: %v", err)
	}
	if result == nil {
		t.Error("expected non-nil result slice")
	}
}

// =============================================
// Session management tests
// =============================================

func TestSessionList(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Create some sessions by writing files
	cli.FileWrite("a1", "s1", "/file1.txt", "data1")
	cli.FileWrite("a1", "s2", "/file2.txt", "data2")

	result, err := cli.SessionList("a1")
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	if result.Total < 2 {
		t.Errorf("expected at least 2 sessions, got %d", result.Total)
	}
	for _, s := range result.Sessions {
		if s.AgentID != "a1" {
			t.Errorf("expected agent_id 'a1', got %q", s.AgentID)
		}
	}
}

func TestSessionListEmpty(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.SessionList("nonexistent-agent")
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	if result.Total != 0 {
		t.Errorf("expected 0 sessions, got %d", result.Total)
	}
}

func TestSessionListMissingAgentID(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SessionList("")
	if err == nil {
		t.Error("expected error for empty agent_id")
	}
	if apiErr, ok := err.(*Error); !ok || apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 error, got %v", err)
	}
}

func TestSessionDelete(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a session
	cli.FileWrite("a1", "del-sess", "/file.txt", "data")

	// Delete it
	err := cli.SessionDelete("a1", "del-sess")
	if err != nil {
		t.Fatalf("SessionDelete failed: %v", err)
	}

	// Verify it's gone — reading should fail or session should not appear in list
	result, err := cli.SessionList("a1")
	if err != nil {
		t.Fatalf("SessionList failed: %v", err)
	}
	for _, s := range result.Sessions {
		if s.SessionID == "del-sess" {
			t.Error("session should have been deleted")
		}
	}
}

func TestSessionDeleteNotFound(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	err := cli.SessionDelete("a1", "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
	if apiErr, ok := err.(*Error); !ok || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 error, got %v", err)
	}
}

// =============================================
// Audit log tests
// =============================================

func TestAuditLogBashExec(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.BashExec("a1", "audit-bash", "echo hello")
	if err != nil {
		t.Fatalf("BashExec failed: %v", err)
	}

	logs, err := cli.SessionGetAuditLogs("a1", "audit-bash", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	if logs.Total < 1 {
		t.Fatalf("expected at least 1 audit entry for bash exec, got %d", logs.Total)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Path == "/v1/bash/exec" && e.Method == "POST" {
			found = true
			if e.Status != 200 {
				t.Errorf("expected status 200, got %d", e.Status)
			}
			break
		}
	}
	if !found {
		t.Error("audit log entry for /v1/bash/exec not found")
	}
}

func TestAuditLogFileWrite(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "audit-file", "/test.txt", "hello")
	if err != nil {
		t.Fatalf("FileWrite failed: %v", err)
	}

	logs, err := cli.SessionGetAuditLogs("a1", "audit-file", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	if logs.Total < 1 {
		t.Fatalf("expected at least 1 audit entry for file write, got %d", logs.Total)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Path == "/v1/file/write" && e.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Error("audit log entry for /v1/file/write not found")
	}
}

func TestAuditLogCodeExecute(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.CodeExecute("a1", "audit-code", "python", "print(1)")
	if err != nil {
		t.Fatalf("CodeExecute failed: %v", err)
	}

	logs, err := cli.SessionGetAuditLogs("a1", "audit-code", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	if logs.Total < 1 {
		t.Fatalf("expected at least 1 audit entry for code execute, got %d", logs.Total)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Path == "/v1/code/execute" && e.Method == "POST" {
			found = true
			if e.Status != 200 {
				t.Errorf("expected status 200, got %d", e.Status)
			}
			break
		}
	}
	if !found {
		t.Error("audit log entry for /v1/code/execute not found")
	}
}

func TestAuditLogMultipleActions(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Perform multiple audited actions in the same session
	cli.BashExec("a1", "audit-multi", "echo 1")
	cli.FileWrite("a1", "audit-multi", "/f.txt", "data")
	cli.BashExec("a1", "audit-multi", "echo 2")

	logs, err := cli.SessionGetAuditLogs("a1", "audit-multi", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	if logs.Total != 3 {
		t.Errorf("expected 3 audit entries, got %d", logs.Total)
	}
}

func TestAuditLogBashWrite(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Start an async command so we have something to write to
	result, err := cli.BashExec("a1", "audit-bw", "cat", WithAsyncMode(true))
	if err != nil {
		t.Fatalf("BashExec async failed: %v", err)
	}

	err = cli.BashWrite("a1", "audit-bw", result.CommandID, "hello\n")
	if err != nil {
		t.Fatalf("BashWrite failed: %v", err)
	}

	logs, err := cli.SessionGetAuditLogs("a1", "audit-bw", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Path == "/v1/bash/write" && e.Method == "POST" {
			found = true
			if e.Status != 200 {
				t.Errorf("expected status 200, got %d", e.Status)
			}
			break
		}
	}
	if !found {
		t.Error("audit log entry for /v1/bash/write not found")
	}
}

func TestAuditLogBashKill(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Start an async command to kill
	_, err := cli.BashExec("a1", "audit-bk", "sleep 60", WithAsyncMode(true))
	if err != nil {
		t.Fatalf("BashExec async failed: %v", err)
	}

	// Kill it — may fail if no process, but audit entry should still be written
	cli.BashKill("a1", "audit-bk", "SIGTERM")

	logs, err := cli.SessionGetAuditLogs("a1", "audit-bk", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Path == "/v1/bash/kill" && e.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Error("audit log entry for /v1/bash/kill not found")
	}
}

func TestAuditLogFileReplace(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	cli.FileWrite("a1", "audit-fr", "/rep.txt", "foo bar foo")

	_, err := cli.FileReplace("a1", "audit-fr", "/rep.txt", "foo", "baz")
	if err != nil {
		t.Fatalf("FileReplace failed: %v", err)
	}

	logs, err := cli.SessionGetAuditLogs("a1", "audit-fr", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Path == "/v1/file/replace" && e.Method == "POST" {
			found = true
			if e.Status != 200 {
				t.Errorf("expected status 200, got %d", e.Status)
			}
			break
		}
	}
	if !found {
		t.Error("audit log entry for /v1/file/replace not found")
	}
}

func TestAuditLogFileUpload(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	content := bytes.NewReader([]byte("audit upload content"))
	_, err := cli.FileUpload("a1", "audit-fu", "/uploaded.txt", content, "test.txt")
	if err != nil {
		t.Fatalf("FileUpload failed: %v", err)
	}

	logs, err := cli.SessionGetAuditLogs("a1", "audit-fu", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	found := false
	for _, e := range logs.Entries {
		if e.Path == "/v1/file/upload" && e.Method == "POST" {
			found = true
			if e.Status != 200 {
				t.Errorf("expected status 200, got %d", e.Status)
			}
			break
		}
	}
	if !found {
		t.Error("audit log entry for /v1/file/upload not found")
	}
}

func TestAuditLogSkillsFallback(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Skills routes have auditMW but no agent_id/session_id in body,
	// so entries should go to fallback log, not per-session files.
	_, err := cli.SkillCreate("audit-skill", "test")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	// No per-session audit should exist
	logs, err := cli.SessionGetAuditLogs("nonexistent-agent", "nonexistent-session", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	if logs.Total != 0 {
		t.Errorf("expected 0 per-session audit entries for skills (should go to fallback), got %d", logs.Total)
	}
}

func TestAuditLogEntryFields(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.BashExec("a1", "audit-fields", "echo check-fields")
	if err != nil {
		t.Fatalf("BashExec failed: %v", err)
	}

	logs, err := cli.SessionGetAuditLogs("a1", "audit-fields", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	if logs.Total < 1 {
		t.Fatalf("expected at least 1 audit entry, got %d", logs.Total)
	}

	e := logs.Entries[0]
	if e.AgentID != "a1" {
		t.Errorf("expected agent_id 'a1', got %q", e.AgentID)
	}
	if e.SessionID != "audit-fields" {
		t.Errorf("expected session_id 'audit-fields', got %q", e.SessionID)
	}
	if e.Method != "POST" {
		t.Errorf("expected method POST, got %q", e.Method)
	}
	if e.Path != "/v1/bash/exec" {
		t.Errorf("expected path /v1/bash/exec, got %q", e.Path)
	}
	if e.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	if e.Latency == "" {
		t.Error("expected non-empty latency")
	}
	if e.Status != 200 {
		t.Errorf("expected status 200, got %d", e.Status)
	}
}

func TestAuditLogNoEntryForUnauditedRoute(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// file/read is not audited
	cli.FileWrite("a1", "audit-noentry", "/r.txt", "x")
	cli.FileRead("a1", "audit-noentry", "/r.txt")

	logs, err := cli.SessionGetAuditLogs("a1", "audit-noentry", 0, 100)
	if err != nil {
		t.Fatalf("SessionGetAuditLogs failed: %v", err)
	}
	// Only the write should be audited, not the read
	if logs.Total != 1 {
		t.Errorf("expected 1 audit entry (write only), got %d", logs.Total)
	}
}

// =============================================
// New option tests
// =============================================

func TestBashExecHardTimeout(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.BashExec("a1", "ht1", "echo ok",
		WithHardTimeout(60),
	)
	if err != nil {
		t.Fatalf("BashExec with HardTimeout failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "ok\n" {
		t.Errorf("expected stdout 'ok\\n', got %v", result.Stdout)
	}
}

func TestBashExecMaxOutputLength(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	result, err := cli.BashExec("a1", "mol1", "echo hello",
		WithMaxOutputLength(1024),
	)
	if err != nil {
		t.Fatalf("BashExec with MaxOutputLength failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "hello\n" {
		t.Errorf("expected stdout 'hello\\n', got %v", result.Stdout)
	}
}

func TestBashCreateSessionWithOptions(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	info, err := cli.BashCreateSession("a1", "opt-sess",
		WithBashSID("custom-bash"),
		WithSessionExecDir("/tmp"),
	)
	if err != nil {
		t.Fatalf("BashCreateSession with options failed: %v", err)
	}
	if info.SessionID != "custom-bash" {
		t.Errorf("expected session_id 'custom-bash', got %s", info.SessionID)
	}
	if info.Status != SessionReady {
		t.Errorf("expected status ready, got %s", info.Status)
	}
}

func TestCodeExecuteWithCwd(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Create a subdirectory inside the session via file write
	cli.FileWrite("a1", "cwd1", "/mydir/placeholder.txt", "x")

	result, err := cli.CodeExecute("a1", "cwd1", "python", "import os; print(os.path.basename(os.getcwd()))",
		WithCwd("/mydir"),
	)
	if err != nil {
		t.Fatalf("CodeExecute with Cwd failed: %v", err)
	}
	if result.Stdout == nil || *result.Stdout != "mydir\n" {
		t.Errorf("expected stdout 'mydir\\n', got %v", result.Stdout)
	}
}

func TestFileWriteWithNewlines(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.FileWrite("a1", "nl1", "/newline.txt", "content",
		WithLeadingNewline(true),
		WithTrailingNewline(true),
	)
	if err != nil {
		t.Fatalf("FileWrite with newlines failed: %v", err)
	}

	result, err := cli.FileRead("a1", "nl1", "/newline.txt")
	if err != nil {
		t.Fatalf("FileRead failed: %v", err)
	}
	if result.Content != "\ncontent\n" {
		t.Errorf("expected '\\ncontent\\n', got %q", result.Content)
	}
}

// =============================================
// New skill API tests
// =============================================

func TestSkillGetViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillCreate("get-test", "A get test skill")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	result, err := cli.SkillGet("get-test")
	if err != nil {
		t.Fatalf("SkillGet failed: %v", err)
	}
	if result.Skill.Name != "get-test" {
		t.Errorf("expected name 'get-test', got %s", result.Skill.Name)
	}
	if result.Skill.Description != "A get test skill" {
		t.Errorf("expected description 'A get test skill', got %s", result.Skill.Description)
	}
}

func TestSkillGetNotFoundViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillGet("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent skill")
	}
	if apiErr, ok := err.(*Error); !ok || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 error, got %v", err)
	}
}

func TestSkillUpdateViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillCreate("upd-test", "original")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	result, err := cli.SkillUpdate("upd-test", "updated desc")
	if err != nil {
		t.Fatalf("SkillUpdate failed: %v", err)
	}
	if result.Skill.Description != "updated desc" {
		t.Errorf("expected 'updated desc', got %s", result.Skill.Description)
	}

	// Verify via get
	getResult, _ := cli.SkillGet("upd-test")
	if getResult.Skill.Description != "updated desc" {
		t.Errorf("get after update: expected 'updated desc', got %s", getResult.Skill.Description)
	}
}

func TestSkillRenameViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillCreate("rename-old", "rename test")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	result, err := cli.SkillRename("rename-old", "rename-new")
	if err != nil {
		t.Fatalf("SkillRename failed: %v", err)
	}
	if result.Skill.Name != "rename-new" {
		t.Errorf("expected name 'rename-new', got %s", result.Skill.Name)
	}

	// Old name should not exist
	_, err = cli.SkillGet("rename-old")
	if err == nil {
		t.Error("expected error for old name after rename")
	}

	// New name should exist
	getResult, err := cli.SkillGet("rename-new")
	if err != nil {
		t.Fatalf("SkillGet after rename failed: %v", err)
	}
	if getResult.Skill.Name != "rename-new" {
		t.Errorf("expected 'rename-new', got %s", getResult.Skill.Name)
	}
}

func TestSkillExportViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillCreate("export-test", "export test")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	_, err = cli.SkillFileWrite("export-test", "data.txt", "export data")
	if err != nil {
		t.Fatalf("SkillFileWrite failed: %v", err)
	}

	body, err := cli.SkillExport("export-test")
	if err != nil {
		t.Fatalf("SkillExport failed: %v", err)
	}
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read export body failed: %v", err)
	}

	// Verify it's a valid ZIP
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("invalid ZIP: %v", err)
	}

	fileNames := make(map[string]bool)
	for _, f := range zr.File {
		fileNames[f.Name] = true
	}
	if !fileNames["data.txt"] {
		t.Error("ZIP should contain data.txt")
	}
	if fileNames["_meta.json"] {
		t.Error("ZIP should NOT contain _meta.json")
	}
}

func TestSkillCopyViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	_, err := cli.SkillCreate("copy-src", "copy test")
	if err != nil {
		t.Fatalf("SkillCreate failed: %v", err)
	}

	_, err = cli.SkillFileWrite("copy-src", "file.txt", "copied content")
	if err != nil {
		t.Fatalf("SkillFileWrite failed: %v", err)
	}

	result, err := cli.SkillCopy("copy-src", "copy-dst")
	if err != nil {
		t.Fatalf("SkillCopy failed: %v", err)
	}
	if result.Skill.Name != "copy-dst" {
		t.Errorf("expected name 'copy-dst', got %s", result.Skill.Name)
	}

	// Verify source still exists
	_, err = cli.SkillGet("copy-src")
	if err != nil {
		t.Errorf("source should still exist: %v", err)
	}

	// Verify destination has the file
	readResult, err := cli.SkillFileRead("copy-dst", "file.txt")
	if err != nil {
		t.Fatalf("SkillFileRead on copy failed: %v", err)
	}
	if readResult.Content != "copied content" {
		t.Errorf("expected 'copied content', got %q", readResult.Content)
	}
}

func TestSkillAgentCacheDeleteViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	// Create and load skills
	for _, name := range []string{"cd-a", "cd-b"} {
		_, err := cli.SkillCreate(name, "test")
		if err != nil {
			t.Fatalf("SkillCreate failed: %v", err)
		}
	}

	_, err := cli.SkillAgentLoad("cd-agent", []string{"cd-a", "cd-b"})
	if err != nil {
		t.Fatalf("SkillAgentLoad failed: %v", err)
	}

	// Delete specific cache
	result, err := cli.SkillAgentCacheDelete("cd-agent", "cd-a")
	if err != nil {
		t.Fatalf("SkillAgentCacheDelete failed: %v", err)
	}
	if len(result.Deleted) != 1 || result.Deleted[0] != "cd-a" {
		t.Errorf("expected deleted=['cd-a'], got %v", result.Deleted)
	}

	// Delete all remaining cache
	result2, err := cli.SkillAgentCacheDelete("cd-agent")
	if err != nil {
		t.Fatalf("SkillAgentCacheDelete all failed: %v", err)
	}
	if len(result2.Deleted) != 1 || result2.Deleted[0] != "cd-b" {
		t.Errorf("expected deleted=['cd-b'], got %v", result2.Deleted)
	}
}

func TestSkillAgentLoadWithCleanupViaClient(t *testing.T) {
	cli, cleanup := setupTestServer(t)
	defer cleanup()

	for _, name := range []string{"cl-keep", "cl-remove"} {
		_, err := cli.SkillCreate(name, "test")
		if err != nil {
			t.Fatalf("SkillCreate failed: %v", err)
		}
	}

	// Load both
	_, err := cli.SkillAgentLoad("cl-agent", []string{"cl-keep", "cl-remove"})
	if err != nil {
		t.Fatalf("SkillAgentLoad failed: %v", err)
	}

	// Load with cleanup, only keeping cl-keep
	_, err = cli.SkillAgentLoad("cl-agent", []string{"cl-keep"}, true)
	if err != nil {
		t.Fatalf("SkillAgentLoad with cleanup failed: %v", err)
	}

	// Verify cl-remove was cleaned up by trying to delete it (should 404)
	_, err = cli.SkillAgentCacheDelete("cl-agent", "cl-remove")
	if err == nil {
		t.Error("expected error for cleaned-up cache")
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
