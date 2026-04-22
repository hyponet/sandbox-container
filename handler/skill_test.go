package handler

import (
	"archive/zip"
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

func setupSkillRouter() (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("sandbox-skill-test-%d-%d", time.Now().UnixNano(), os.Getpid()))
	os.MkdirAll(dir, 0755)
	globalSkillsDir := filepath.Join(dir, "global-skills")
	os.MkdirAll(globalSkillsDir, 0755)

	mgr := session.NewManager(dir, 24*time.Hour)
	mgr.SetGlobalSkillsRoot(globalSkillsDir)

	r := gin.New()
	skillH := NewSkillHandler(mgr)

	agents := r.Group("/v1/skills/agents")
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
		agents.DELETE("/:agent_id/cache", skillH.AgentCacheDelete)
	}

	return r, mgr
}

// setupSkillRouterWithGlobalRoutes sets up a router with both global skills routes
// (via registry) and agent routes, for integration tests that need global skill creation.
// Uses the registry handler for global skill management.
func setupSkillRouterWithGlobalRoutes() (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("sandbox-skill-global-test-%d-%d", time.Now().UnixNano(), os.Getpid()))
	os.MkdirAll(dir, 0755)
	globalSkillsDir := filepath.Join(dir, "global-skills")
	os.MkdirAll(globalSkillsDir, 0755)
	registryDir := filepath.Join(dir, "registry")
	os.MkdirAll(registryDir, 0755)

	mgr := session.NewManager(dir, 24*time.Hour)
	mgr.SetGlobalSkillsRoot(globalSkillsDir)
	mgr.SetRegistryRoot(registryDir)

	r := gin.New()
	skillH := NewSkillHandler(mgr)
	registryH := NewRegistryHandler(mgr)
	registryH.SetSSRFProtection(false) // disable SSRF for tests using httptest (loopback)

	// Registry routes for creating/managing skills globally
	registry := r.Group("/v1/registry")
	{
		registry.POST("/create", registryH.Create)
		registry.POST("/get", registryH.Get)
		registry.POST("/update", registryH.Update)
		registry.POST("/delete", registryH.Delete)
		registry.POST("/list", registryH.List)
		registry.POST("/rename", registryH.Rename)
		registry.POST("/copy", registryH.Copy)
		registry.POST("/import", registryH.Import)
		registry.POST("/import/upload", registryH.ImportUpload)
		registry.GET("/export", registryH.Export)
		registry.POST("/versions/create", registryH.VersionCreate)
		registry.POST("/versions/get", registryH.VersionGet)
		registry.POST("/versions/list", registryH.VersionList)
		registry.POST("/versions/delete", registryH.VersionDelete)
		registry.POST("/versions/tree", registryH.VersionTree)
		registry.POST("/versions/file/read", registryH.VersionFileRead)
		registry.POST("/versions/file/write", registryH.VersionFileWrite)
		registry.POST("/versions/file/update", registryH.VersionFileUpdate)
		registry.POST("/versions/file/mkdir", registryH.VersionFileMkdir)
		registry.POST("/versions/file/delete", registryH.VersionFileDelete)
		registry.POST("/activate", registryH.Activate)
		registry.POST("/commit", registryH.Commit)
	}

	// Agent routes
	agents := r.Group("/v1/skills/agents")
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
		agents.DELETE("/:agent_id/cache", skillH.AgentCacheDelete)
	}

	return r, mgr
}

// createTestZip creates a test ZIP file with the given files.
func createTestZip(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	w.Close()

	tmpFile, err := os.CreateTemp("", "test-skill-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Write(buf.Bytes())
	tmpFile.Close()
	return tmpFile.Name()
}

// createSkillViaRegistry is a test helper that creates a skill in the registry,
// creates a version, writes SKILLS.md, and activates it.
func createSkillViaRegistry(t *testing.T, r *gin.Engine, name, description string) {
	t.Helper()

	// Create registry entry
	w := doRequest(t, r, "POST", "/v1/registry/create",
		fmt.Sprintf(`{"name": "%s", "description": "%s"}`, name, description))
	if w.Code != http.StatusOK {
		t.Fatalf("registry create failed for %s: %d %s", name, w.Code, w.Body.String())
	}

	// Create version
	w = doRequest(t, r, "POST", "/v1/registry/versions/create",
		fmt.Sprintf(`{"name": "%s"}`, name))
	if w.Code != http.StatusOK {
		t.Fatalf("version create failed for %s: %d %s", name, w.Code, w.Body.String())
	}

	// Parse version from response
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	version := data["version"].(map[string]interface{})["version"].(string)

	// Write SKILLS.md to version — use json.Marshal for content to ensure proper escaping
	mdContent := fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n%s skill content.", name, description, name)
	contentBytes, _ := json.Marshal(mdContent)
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "%s", "version": "%s", "path": "SKILLS.md", "content": %s}`, name, version, string(contentBytes)))
	if w.Code != http.StatusOK {
		t.Fatalf("write SKILLS.md failed for %s: %d %s", name, w.Code, w.Body.String())
	}

	// Activate version
	w = doRequest(t, r, "POST", "/v1/registry/activate",
		fmt.Sprintf(`{"name": "%s", "version": "%s"}`, name, version))
	if w.Code != http.StatusOK {
		t.Fatalf("activate failed for %s: %d %s", name, w.Code, w.Body.String())
	}
}

// =============================================
// Agent Skill Tests
// =============================================

func TestAgentSkillLoad(t *testing.T) {
	r, _ := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "load-skill", "test load")

	// Load into agent via route
	loadBody := `{"skill_ids": ["load-skill"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("load failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skills := data["skills"].([]interface{})
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	skill := skills[0].(map[string]interface{})
	if skill["name"] != "load-skill" {
		t.Errorf("expected name 'load-skill', got %v", skill["name"])
	}
	// Content should be body only (no frontmatter)
	content := skill["content"].(string)
	if strings.Contains(content, "---") {
		t.Error("expected content without frontmatter delimiters")
	}
}

func TestAgentSkillList(t *testing.T) {
	r, _ := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "list-skill", "test list")

	// List via route
	listBody := `{"skill_ids": ["list-skill"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/list", bytes.NewBufferString(listBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skills := data["skills"].([]interface{})
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	skill := skills[0].(map[string]interface{})
	if skill["name"] != "list-skill" {
		t.Errorf("expected name 'list-skill', got %v", skill["name"])
	}
	if skill["path"] != "/skills/list-skill" {
		t.Errorf("expected path '/skills/list-skill', got %v", skill["path"])
	}
	fm := skill["frontmatter"].(string)
	if !strings.Contains(fm, "name: list-skill") {
		t.Errorf("expected frontmatter to contain 'name: list-skill', got %q", fm)
	}
	if strings.Contains(fm, "---") {
		t.Error("frontmatter should not contain --- delimiters")
	}
}

func TestAgentSkillLoadCaching(t *testing.T) {
	r, _ := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "cache-skill", "test")

	// First load
	loadBody := `{"skill_ids": ["cache-skill"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first load failed: %d", w.Code)
	}

	// Just call load again - should still work
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("second load failed: %d", w.Code)
	}
}

func TestAgentSkillLoadNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"skill_ids": ["nonexistent"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Handler skips nonexistent skills and returns 200 with empty results
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skills := data["skills"].([]interface{})
	if len(skills) != 0 {
		t.Errorf("expected 0 skills for nonexistent, got %d", len(skills))
	}
}

func TestAgentSkillLoadImportedFrontmatterSplit(t *testing.T) {
	r, _ := setupSkillRouterWithGlobalRoutes()

	// Create skill via registry import
	zipPath := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: rich-skill\ndescription: A rich skill\ntags: [a, b]\n---\n## Instructions\nDo things.",
	})
	defer os.Remove(zipPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipPath)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	importBody := `{"name": "rich-skill", "zip_url": "` + server.URL + `/skill.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/registry/import", bytes.NewBufferString(importBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", w.Code, w.Body.String())
	}

	// Test list returns frontmatter only
	listBody := `{"skill_ids": ["rich-skill"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/list", bytes.NewBufferString(listBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	var listResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &listResp)
	listData := listResp["data"].(map[string]interface{})
	listSkills := listData["skills"].([]interface{})
	fm := listSkills[0].(map[string]interface{})["frontmatter"].(string)
	if !strings.Contains(fm, "tags:") {
		t.Errorf("expected frontmatter to contain 'tags:', got %q", fm)
	}
	if strings.Contains(fm, "Instructions") {
		t.Error("frontmatter should not contain body content")
	}

	// Test load returns body only
	loadBody := `{"skill_ids": ["rich-skill"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("load failed: %d %s", w.Code, w.Body.String())
	}

	var loadResp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &loadResp)
	loadData := loadResp["data"].(map[string]interface{})
	loadSkills := loadData["skills"].([]interface{})
	content := loadSkills[0].(map[string]interface{})["content"].(string)
	if !strings.Contains(content, "## Instructions") {
		t.Errorf("expected body to contain '## Instructions', got %q", content)
	}
	if strings.Contains(content, "---") {
		t.Error("body should not contain frontmatter delimiters")
	}
	if strings.Contains(content, "tags:") {
		t.Error("body should not contain frontmatter fields")
	}
}

func TestAgentSkillCleanup(t *testing.T) {
	r, mgr := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "keep-skill", "test")
	createSkillViaRegistry(t, r, "remove-skill", "test")

	// Load both into agent
	loadBody := `{"skill_ids": ["keep-skill", "remove-skill"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/cleanup-agent/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("load failed: %d", w.Code)
	}

	// Verify both cached
	for _, name := range []string{"keep-skill", "remove-skill"} {
		if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("cleanup-agent"), name)); err != nil {
			t.Fatalf("skill %s not cached: %v", name, err)
		}
	}

	// Load with cleanup=true, only requesting keep-skill
	cleanupBody := `{"skill_ids": ["keep-skill"], "cleanup": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/cleanup-agent/load", bytes.NewBufferString(cleanupBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cleanup load failed: %d", w.Code)
	}

	// keep-skill should still be cached
	if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("cleanup-agent"), "keep-skill")); err != nil {
		t.Errorf("keep-skill should still be cached: %v", err)
	}
	// remove-skill should be gone
	if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("cleanup-agent"), "remove-skill")); err == nil {
		t.Error("remove-skill should have been cleaned up")
	}
}

func TestAgentSkillListCleanup(t *testing.T) {
	r, mgr := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "list-keep", "test")
	createSkillViaRegistry(t, r, "list-remove", "test")

	// Load both into agent
	loadBody := `{"skill_ids": ["list-keep", "list-remove"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/list-cleanup/list", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// List with cleanup=true, only requesting list-keep
	cleanupBody := `{"skill_ids": ["list-keep"], "cleanup": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/list-cleanup/list", bytes.NewBufferString(cleanupBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cleanup list failed: %d", w.Code)
	}

	// list-remove should be gone
	if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("list-cleanup"), "list-remove")); err == nil {
		t.Error("list-remove should have been cleaned up")
	}
}

func TestAgentCacheDelete(t *testing.T) {
	r, mgr := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "cache-a", "test")
	createSkillViaRegistry(t, r, "cache-b", "test")

	loadBody := `{"skill_ids": ["cache-a", "cache-b"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/del-agent/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Delete specific skill cache
	req = httptest.NewRequest(http.MethodDelete, "/v1/skills/agents/del-agent/cache?skill_id=cache-a", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cache delete failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	deleted := data["deleted"].([]interface{})
	if len(deleted) != 1 || deleted[0] != "cache-a" {
		t.Errorf("expected deleted=['cache-a'], got %v", deleted)
	}

	// cache-a should be gone, cache-b should remain
	if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("del-agent"), "cache-a")); err == nil {
		t.Error("cache-a should have been deleted")
	}
	if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("del-agent"), "cache-b")); err != nil {
		t.Errorf("cache-b should still exist: %v", err)
	}
}

func TestAgentCacheDeleteAll(t *testing.T) {
	r, mgr := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "cache-all", "test")

	loadBody := `{"skill_ids": ["cache-all"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/agents/del-all-agent/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Delete all cache
	req = httptest.NewRequest(http.MethodDelete, "/v1/skills/agents/del-all-agent/cache", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("cache delete all failed: %d %s", w.Code, w.Body.String())
	}

	// All cache should be gone
	entries, _ := os.ReadDir(mgr.SkillsRoot("del-all-agent"))
	dirs := 0
	for _, e := range entries {
		if e.IsDir() {
			dirs++
		}
	}
	if dirs != 0 {
		t.Errorf("expected 0 cached skills, got %d", dirs)
	}
}

func TestAgentCacheDeleteNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	req := httptest.NewRequest(http.MethodDelete, "/v1/skills/agents/no-agent/cache?skill_id=nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// Fix #7: AgentCacheDelete validates agentID
func TestAgentCacheDeleteInvalidAgentID(t *testing.T) {
	r, _ := setupSkillRouter()

	req := httptest.NewRequest(http.MethodDelete, "/v1/skills/agents/../../etc/cache", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// gin may route this differently, but if it reaches the handler, it should reject
	// We accept either 400 (validation) or 404 (gin routing mismatch)
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Errorf("expected 400 or 404 for path traversal agent_id, got %d", w.Code)
	}
}

// =============================================
// Helper function tests
// =============================================

func TestSplitFrontmatter(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantFM string
		wantB  string
	}{
		{"with frontmatter", "---\nname: x\n---\nbody here", "name: x", "body here"},
		{"no frontmatter", "just content", "", "just content"},
		{"only opening", "---\nname: x\nno closing", "", "---\nname: x\nno closing"},
		{"empty body", "---\nname: x\n---\n", "name: x", ""},
		{"leading newlines trimmed", "---\nk: v\n---\n\n\nbody", "k: v", "body"},
		{"empty frontmatter and body", "---\n---", "", ""},
		{"empty frontmatter with body", "---\n---\ncontent", "", "content"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body := splitFrontmatter(tt.input)
			if fm != tt.wantFM {
				t.Errorf("frontmatter: got %q, want %q", fm, tt.wantFM)
			}
			if body != tt.wantB {
				t.Errorf("body: got %q, want %q", body, tt.wantB)
			}
		})
	}
}

// Test quoteYAMLDescription helper
func TestQuoteYAMLDescription(t *testing.T) {
	tests := []struct {
		input    string
		contains string // expected substring in output
		notIn    string // should NOT appear unescaped
	}{
		{`simple`, `"simple"`, ""},
		{`has "quotes"`, `\"quotes\"`, ""},
		{"has\nnewline", `\n`, ""},
		{`has\backslash`, `\\`, ""},
		{"tab\there", `\t`, ""},
	}
	for _, tt := range tests {
		result := quoteYAMLDescription(tt.input)
		if !strings.Contains(result, tt.contains) {
			t.Errorf("quoteYAMLDescription(%q) = %s, expected to contain %q", tt.input, result, tt.contains)
		}
	}
}

// Test buildSkillsMDContent helper
func TestBuildSkillsMDContent(t *testing.T) {
	result := buildSkillsMDContent("my-skill", "A test skill", "## Body\nContent here.")
	if !strings.HasPrefix(result, "---\n") {
		t.Error("expected frontmatter opening")
	}
	if !strings.Contains(result, "name: my-skill") {
		t.Error("expected name in frontmatter")
	}
	if !strings.Contains(result, `description: "A test skill"`) {
		t.Errorf("expected quoted description, got:\n%s", result)
	}
	if !strings.Contains(result, "## Body\nContent here.") {
		t.Error("expected body content preserved")
	}
}

// Test findSkillsMDFile helper
func TestFindSkillsMDFile(t *testing.T) {
	r, mgr := setupSkillRouterWithGlobalRoutes()

	createSkillViaRegistry(t, r, "find-md", "test")

	skillDir := mgr.GlobalSkillPath("find-md")
	p, content, err := findSkillsMDFile(skillDir)
	if err != nil {
		t.Fatalf("findSkillsMDFile failed: %v", err)
	}
	if filepath.Base(p) != "SKILLS.md" {
		t.Errorf("expected SKILLS.md, got %s", filepath.Base(p))
	}
	if content == "" {
		t.Error("expected non-empty content")
	}

	// Non-existent skill dir
	_, _, err = findSkillsMDFile("/nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent dir")
	}
}

// =============================================
// extractZip tests (shared utility)
// =============================================

func TestExtractZipNestedDirsWithoutDirFlag(t *testing.T) {
	// Simulate zip files where directory entries lack the proper directory flag
	// but are indicated by a trailing "/" in the name (common on Windows zip tools).
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// SKILLS.md at root (flat layout)
	f, err := w.Create("SKILLS.md")
	if err != nil {
		t.Fatalf("Create SKILLS.md: %v", err)
	}
	if _, err := f.Write([]byte("---\nname: test\n---\n")); err != nil {
		t.Fatalf("Write SKILLS.md: %v", err)
	}

	// Create directory entry WITHOUT setting ModeDir — only the trailing "/" identifies it
	fh := &zip.FileHeader{Name: "scripts/", Method: zip.Deflate}
	fh.SetMode(0644) // regular file mode, NOT a directory
	if _, err := w.CreateHeader(fh); err != nil {
		t.Fatalf("CreateHeader scripts/: %v", err)
	}

	// File inside that directory
	f, err = w.Create("scripts/run_bioinfor.py")
	if err != nil {
		t.Fatalf("Create scripts/run_bioinfor.py: %v", err)
	}
	if _, err := f.Write([]byte("#!/usr/bin/env python3\nprint('hello')\n")); err != nil {
		t.Fatalf("Write run_bioinfor.py: %v", err)
	}

	// Another nested level
	fh2 := &zip.FileHeader{Name: "scripts/sub/", Method: zip.Deflate}
	fh2.SetMode(0644)
	if _, err := w.CreateHeader(fh2); err != nil {
		t.Fatalf("CreateHeader scripts/sub/: %v", err)
	}

	f3, err := w.Create("scripts/sub/deep.txt")
	if err != nil {
		t.Fatalf("Create scripts/sub/deep.txt: %v", err)
	}
	if _, err := f3.Write([]byte("deep file")); err != nil {
		t.Fatalf("Write deep.txt: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("zip.Writer.Close: %v", err)
	}

	tmpZip, err := os.CreateTemp("", "nodirflag-*.zip")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := tmpZip.Write(buf.Bytes()); err != nil {
		t.Fatalf("Write zip: %v", err)
	}
	tmpZip.Close()
	defer os.Remove(tmpZip.Name())

	destDir, err := os.MkdirTemp("", "extract-dest-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(destDir)

	if err := extractZip(tmpZip.Name(), destDir); err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}

	// Verify files were extracted correctly
	for _, path := range []string{
		"SKILLS.md",
		"scripts/run_bioinfor.py",
		"scripts/sub/deep.txt",
	} {
		fullPath := filepath.Join(destDir, path)
		if _, err := os.Stat(fullPath); err != nil {
			t.Errorf("%s should exist: %v", path, err)
		}
	}

	// Verify scripts is a directory, not a file
	info, err := os.Stat(filepath.Join(destDir, "scripts"))
	if err != nil {
		t.Fatalf("scripts should exist: %v", err)
	}
	if !info.IsDir() {
		t.Error("scripts should be a directory, not a regular file")
	}
}

func TestExtractZipPermissionMasking(t *testing.T) {
	// Verify that extracted files have permissions masked to 0755 max,
	// preventing setuid/setgid/sticky bits or overly permissive modes.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// SKILLS.md required
	f, _ := w.Create("SKILLS.md")
	f.Write([]byte("---\nname: test\n---\n"))

	// Create a file with setuid + 0777 permissions
	fh := &zip.FileHeader{Name: "dangerous.sh", Method: zip.Deflate}
	fh.SetMode(os.FileMode(0o4777)) // setuid + rwxrwxrwx
	fw, err := w.CreateHeader(fh)
	if err != nil {
		t.Fatalf("CreateHeader: %v", err)
	}
	if _, err := fw.Write([]byte("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("zip.Writer.Close: %v", err)
	}

	tmpZip, err := os.CreateTemp("", "permmask-*.zip")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := tmpZip.Write(buf.Bytes()); err != nil {
		t.Fatalf("Write zip: %v", err)
	}
	tmpZip.Close()
	defer os.Remove(tmpZip.Name())

	destDir, err := os.MkdirTemp("", "permmask-dest-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(destDir)

	if err := extractZip(tmpZip.Name(), destDir); err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(destDir, "dangerous.sh"))
	if err != nil {
		t.Fatalf("dangerous.sh should exist: %v", err)
	}

	perm := info.Mode().Perm()
	if perm&os.ModeSetuid != 0 || perm&os.ModeSetgid != 0 || perm&os.ModeSticky != 0 {
		t.Errorf("setuid/setgid/sticky bits should be stripped, got %v", info.Mode())
	}
	if perm > 0755 {
		t.Errorf("permissions should be at most 0755, got %04o", perm)
	}
}

func TestExtractZipPathTraversal(t *testing.T) {
	// Create a ZIP with a path traversal entry
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	// SKILLS.md required
	f, _ := w.Create("SKILLS.md")
	f.Write([]byte("---\nname: test\n---\n"))
	// Normal file
	f, _ = w.Create("normal.txt")
	f.Write([]byte("safe"))
	// Path traversal file (should be skipped)
	f2, _ := w.Create("../escape.txt")
	f2.Write([]byte("escaped"))
	w.Close()

	tmpZip, _ := os.CreateTemp("", "traversal-*.zip")
	tmpZip.Write(buf.Bytes())
	tmpZip.Close()
	defer os.Remove(tmpZip.Name())

	destDir, _ := os.MkdirTemp("", "extract-dest-*")
	defer os.RemoveAll(destDir)

	err := extractZip(tmpZip.Name(), destDir)
	if err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}

	// normal.txt should exist
	if _, err := os.Stat(filepath.Join(destDir, "normal.txt")); err != nil {
		t.Errorf("normal.txt should exist: %v", err)
	}

	// ../escape.txt should NOT have been extracted outside destDir
	if _, err := os.Stat(filepath.Join(destDir, "..", "escape.txt")); err == nil {
		t.Error("path traversal file should not have been extracted outside destDir")
	}
}

func TestExtractZipMissingSkillsMD(t *testing.T) {
	// ZIP without SKILLS.md should return an error.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("readme.txt")
	f.Write([]byte("no skills here"))
	w.Close()

	tmpZip, _ := os.CreateTemp("", "noskills-*.zip")
	tmpZip.Write(buf.Bytes())
	tmpZip.Close()
	defer os.Remove(tmpZip.Name())

	destDir, _ := os.MkdirTemp("", "extract-dest-*")
	defer os.RemoveAll(destDir)

	err := extractZip(tmpZip.Name(), destDir)
	if err == nil {
		t.Fatal("expected error when ZIP has no SKILLS.md")
	}
	if !strings.Contains(err.Error(), "SKILLS.md") {
		t.Errorf("error should mention SKILLS.md, got: %v", err)
	}
}

func TestExtractZipWrappedLayout(t *testing.T) {
	// ZIP with a single wrapping folder: my-skill/SKILLS.md, my-skill/scripts/run.sh
	// Should extract SKILLS.md and scripts/run.sh to the root.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	fh := &zip.FileHeader{Name: "my-skill/"}
	fh.SetMode(os.ModeDir | 0755)
	w.CreateHeader(fh)

	f, _ := w.Create("my-skill/SKILLS.md")
	f.Write([]byte("---\nname: wrapped\n---\nhello"))

	f2, _ := w.Create("my-skill/scripts/run.sh")
	f2.Write([]byte("#!/bin/sh\necho hi\n"))

	w.Close()

	tmpZip, _ := os.CreateTemp("", "wrapped-*.zip")
	tmpZip.Write(buf.Bytes())
	tmpZip.Close()
	defer os.Remove(tmpZip.Name())

	destDir, _ := os.MkdirTemp("", "extract-dest-*")
	defer os.RemoveAll(destDir)

	if err := extractZip(tmpZip.Name(), destDir); err != nil {
		t.Fatalf("extractZip failed: %v", err)
	}

	// Files should be at the root, not under my-skill/
	for _, path := range []string{"SKILLS.md", "scripts/run.sh"} {
		fullPath := filepath.Join(destDir, path)
		if _, err := os.Stat(fullPath); err != nil {
			t.Errorf("%s should exist at root level: %v", path, err)
		}
	}

	// my-skill/ should NOT exist as a directory
	if _, err := os.Stat(filepath.Join(destDir, "my-skill")); err == nil {
		t.Error("my-skill/ should not exist — wrapper should have been stripped")
	}
}

func TestExtractZipRejectsNestedSkillsMD(t *testing.T) {
	// ZIP with nested: my-skill/sub/SKILLS.md should be rejected because
	// the import pipeline expects SKILLS.md at the root after any wrapper is stripped.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	fh := &zip.FileHeader{Name: "my-skill/"}
	fh.SetMode(os.ModeDir | 0755)
	w.CreateHeader(fh)

	fh2 := &zip.FileHeader{Name: "my-skill/sub/"}
	fh2.SetMode(os.ModeDir | 0755)
	w.CreateHeader(fh2)

	f, _ := w.Create("my-skill/sub/SKILLS.md")
	f.Write([]byte("---\nname: nested\n---\n"))

	w.Close()

	tmpZip, _ := os.CreateTemp("", "nested-wrap-*.zip")
	tmpZip.Write(buf.Bytes())
	tmpZip.Close()
	defer os.Remove(tmpZip.Name())

	destDir, _ := os.MkdirTemp("", "extract-dest-*")
	defer os.RemoveAll(destDir)

	err := extractZip(tmpZip.Name(), destDir)
	if err == nil {
		t.Fatal("expected nested SKILLS.md layout to be rejected")
	}
	if !strings.Contains(err.Error(), "SKILLS.md") {
		t.Fatalf("expected error to mention SKILLS.md layout, got %v", err)
	}
}

// Verify session manager NewManager handles root creation gracefully.
func TestNewManagerWithTempDir(t *testing.T) {
	dir := t.TempDir()
	mgr := session.NewManager(dir, time.Hour)
	if mgr == nil {
		t.Fatal("manager should not be nil")
	}

	// Verify root was created
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("root dir should exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("root should be a directory")
	}
}

// =============================================
// createTestZipBytes helper
// =============================================

// createTestZipBytes creates a ZIP archive in memory and returns the bytes.
func createTestZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	w.Close()
	return buf.Bytes()
}
