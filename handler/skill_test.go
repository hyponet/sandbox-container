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

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/projectdata"
	"github.com/hyponet/sandbox-container/session"
	"github.com/hyponet/sandbox-container/userdata"

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
	skillH := NewSkillHandler(mgr, nil, nil)

	agents := r.Group("/v1/skills/agents")
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
		agents.DELETE("/:agent_id/cache", skillH.AgentCacheDelete)
	}

	return r, mgr
}

func setupSkillRouterWithLayers() (*gin.Engine, *session.Manager, *userdata.Manager, *projectdata.Manager) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("sandbox-skill-layers-test-%d-%d", time.Now().UnixNano(), os.Getpid()))
	os.MkdirAll(dir, 0755)
	globalSkillsDir := filepath.Join(dir, "global-skills")
	os.MkdirAll(globalSkillsDir, 0755)

	mgr := session.NewManager(filepath.Join(dir, "agents"), 24*time.Hour)
	mgr.SetGlobalSkillsRoot(globalSkillsDir)
	udMgr := userdata.NewManager(filepath.Join(dir, "users"))
	pdMgr := projectdata.NewManager(filepath.Join(dir, "projects"))

	r := gin.New()
	skillH := NewSkillHandler(mgr, udMgr, pdMgr)

	agents := r.Group("/v1/skills/agents")
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
		agents.DELETE("/:agent_id/cache", skillH.AgentCacheDelete)
	}

	return r, mgr, udMgr, pdMgr
}

func writeLayerSkill(t *testing.T, root, name, content string) {
	t.Helper()
	skillDir := filepath.Join(root, "skills", name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write SKILLS.md: %v", err)
	}
}

func writeGlobalSkill(t *testing.T, mgr *session.Manager, name, content string) {
	t.Helper()
	skillDir := mgr.GlobalSkillPath(name)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("mkdir global skill dir: %v", err)
	}
	now := time.Now().UnixNano()
	if err := writeSkillMeta(skillDir, &model.SkillMetaJSON{Name: name, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("write skill meta: %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write global SKILLS.md: %v", err)
	}
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
	skillH := NewSkillHandler(mgr, nil, nil)
	registryH := NewRegistryHandler(mgr, nil, nil)
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

func TestAgentSkillListLoad_LayerPriorityAndFullUserProjectDiscovery(t *testing.T) {
	r, mgr, udMgr, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "shared", "---\nname: shared\n---\nuser shared")
	writeLayerSkill(t, udMgr.Root("u1"), "user-only", "---\nname: user-only\n---\nuser only")
	writeLayerSkill(t, pdMgr.Root("p1"), "shared", "---\nname: shared\n---\nproject shared")
	writeLayerSkill(t, pdMgr.Root("p1"), "project-only", "---\nname: project-only\n---\nproject only")
	writeGlobalSkill(t, mgr, "shared", "---\nname: shared\n---\nagent shared")
	writeGlobalSkill(t, mgr, "agent-only", "---\nname: agent-only\n---\nagent only")

	body := `{"skill_ids":["shared","agent-only"],"user_id":"u1","project_id":"p1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 4 {
		t.Fatalf("expected 4 skills, got %d: %+v", got, listResp.Data.Skills)
	}

	byName := make(map[string]model.SkillSummary)
	for _, skill := range listResp.Data.Skills {
		byName[skill.Name] = skill
	}
	assertSkillSummary := func(name, source, path string, writable bool) {
		t.Helper()
		skill, ok := byName[name]
		if !ok {
			t.Fatalf("missing skill %s in %+v", name, listResp.Data.Skills)
		}
		if skill.Source != source || skill.Path != path || skill.Writable != writable {
			t.Fatalf("skill %s = source:%s path:%s writable:%v", name, skill.Source, skill.Path, skill.Writable)
		}
	}
	assertSkillSummary("shared", "user", "/userdata/skills/shared", true)
	assertSkillSummary("user-only", "user", "/userdata/skills/user-only", true)
	assertSkillSummary("project-only", "project", "/projectdata/skills/project-only", true)
	assertSkillSummary("agent-only", "agent", "/skills/agent-only", false)

	w = doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/load", body)
	if w.Code != http.StatusOK {
		t.Fatalf("load failed: %d %s", w.Code, w.Body.String())
	}
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal load response: %v", err)
	}
	loaded := make(map[string]model.SkillContent)
	for _, skill := range loadResp.Data.Skills {
		loaded[skill.Name] = skill
	}
	if loaded["shared"].Source != "user" || loaded["shared"].Content != "user shared" {
		t.Fatalf("expected shared to load from user layer, got %+v", loaded["shared"])
	}
	if loaded["user-only"].Content != "user only" || loaded["project-only"].Content != "project only" || loaded["agent-only"].Content != "agent only" {
		t.Fatalf("unexpected loaded skills: %+v", loaded)
	}
}

func TestAgentSkillListLoad_RejectInvalidLayerIDs(t *testing.T) {
	r, _, _, _ := setupSkillRouterWithLayers()

	for _, path := range []string{"/v1/skills/agents/a1/list", "/v1/skills/agents/a1/load"} {
		w := doRequest(t, r, http.MethodPost, path, `{"skill_ids":["s"],"user_id":"../evil"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400 for invalid user_id, got %d %s", path, w.Code, w.Body.String())
		}

		w = doRequest(t, r, http.MethodPost, path, `{"skill_ids":["s"],"project_id":"../evil"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: expected 400 for invalid project_id, got %d %s", path, w.Code, w.Body.String())
		}
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

// =============================================
// Skills Load — Single-Layer Isolation Tests
// =============================================

func TestAgentSkillList_UserOnlyNoProjectNoAgent(t *testing.T) {
	r, _, udMgr, _ := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "alpha", "---\nname: alpha\n---\nalpha body")
	writeLayerSkill(t, udMgr.Root("u1"), "beta", "---\nname: beta\n---\nbeta body")

	body := `{"skill_ids":[],"user_id":"u1"}`
	// List
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 2 {
		t.Fatalf("expected 2 skills, got %d", got)
	}
	for _, s := range listResp.Data.Skills {
		if s.Source != "user" || !s.Writable || !strings.HasPrefix(s.Path, "/userdata/skills/") {
			t.Errorf("skill %s: source=%s writable=%v path=%s", s.Name, s.Source, s.Writable, s.Path)
		}
		if strings.Contains(s.Frontmatter, "---") {
			t.Errorf("skill %s: frontmatter should not contain ---", s.Name)
		}
	}

	// Load
	w = doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/load", body)
	if w.Code != http.StatusOK {
		t.Fatalf("load failed: %d %s", w.Code, w.Body.String())
	}
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(loadResp.Data.Skills); got != 2 {
		t.Fatalf("expected 2 skills on load, got %d", got)
	}
	byName := make(map[string]model.SkillContent)
	for _, s := range loadResp.Data.Skills {
		byName[s.Name] = s
	}
	if byName["alpha"].Content != "alpha body" || byName["alpha"].Source != "user" {
		t.Errorf("alpha: %+v", byName["alpha"])
	}
	if strings.Contains(byName["alpha"].Content, "---") {
		t.Error("load content should not contain frontmatter delimiters")
	}
}

func TestAgentSkillList_ProjectOnlyNoUserNoAgent(t *testing.T) {
	r, _, _, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, pdMgr.Root("p1"), "p-alpha", "---\nname: p-alpha\n---\nproject alpha body")
	writeLayerSkill(t, pdMgr.Root("p1"), "p-beta", "---\nname: p-beta\n---\nproject beta body")

	body := `{"skill_ids":[],"project_id":"p1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 2 {
		t.Fatalf("expected 2 skills, got %d", got)
	}
	for _, s := range listResp.Data.Skills {
		if s.Source != "project" || !s.Writable || !strings.HasPrefix(s.Path, "/projectdata/skills/") {
			t.Errorf("skill %s: source=%s writable=%v path=%s", s.Name, s.Source, s.Writable, s.Path)
		}
	}
}

func TestAgentSkillList_AgentOnlyViaLayersRouter(t *testing.T) {
	r, mgr, _, _ := setupSkillRouterWithLayers()

	writeGlobalSkill(t, mgr, "ag-a", "---\nname: ag-a\n---\nagent a body")
	writeGlobalSkill(t, mgr, "ag-b", "---\nname: ag-b\n---\nagent b body")

	body := `{"skill_ids":["ag-a","ag-b"]}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 2 {
		t.Fatalf("expected 2 skills, got %d", got)
	}
	for _, s := range listResp.Data.Skills {
		if s.Source != "agent" || s.Writable || !strings.HasPrefix(s.Path, "/skills/") {
			t.Errorf("skill %s: source=%s writable=%v path=%s", s.Name, s.Source, s.Writable, s.Path)
		}
	}
}

// =============================================
// Skills Load — Two-Layer Overlap Tests
// =============================================

func TestAgentSkillList_UserProjectOverlapNoAgent(t *testing.T) {
	r, _, udMgr, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "overlap", "---\nname: overlap\n---\nuser overlap body")
	writeLayerSkill(t, pdMgr.Root("p1"), "overlap", "---\nname: overlap\n---\nproject overlap body")
	writeLayerSkill(t, pdMgr.Root("p1"), "proj-extra", "---\nname: proj-extra\n---\nproject extra body")

	body := `{"skill_ids":[],"user_id":"u1","project_id":"p1"}`

	// List
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 2 {
		t.Fatalf("expected 2 skills, got %d: %+v", got, listResp.Data.Skills)
	}
	byName := make(map[string]model.SkillSummary)
	for _, s := range listResp.Data.Skills {
		byName[s.Name] = s
	}
	if byName["overlap"].Source != "user" {
		t.Errorf("overlap should come from user layer, got source=%s", byName["overlap"].Source)
	}
	if byName["proj-extra"].Source != "project" {
		t.Errorf("proj-extra should come from project layer, got source=%s", byName["proj-extra"].Source)
	}

	// Load — verify overlap content comes from user layer
	w = doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/load", body)
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	loaded := make(map[string]model.SkillContent)
	for _, s := range loadResp.Data.Skills {
		loaded[s.Name] = s
	}
	if loaded["overlap"].Content != "user overlap body" {
		t.Errorf("overlap body should come from user layer, got %q", loaded["overlap"].Content)
	}
}

// =============================================
// Skills Load — Empty skill_ids with Layer Skills
// =============================================

func TestAgentSkillList_EmptySkillIDsWithUserProjectSkills(t *testing.T) {
	r, _, udMgr, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "us", "---\nname: us\n---\nuser skill")
	writeLayerSkill(t, pdMgr.Root("p1"), "ps", "---\nname: ps\n---\nproject skill")

	body := `{"skill_ids":[],"user_id":"u1","project_id":"p1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 2 {
		t.Fatalf("expected 2 skills discovered by scanning, got %d", got)
	}
	sources := make(map[string]bool)
	for _, s := range listResp.Data.Skills {
		sources[s.Source] = true
	}
	if !sources["user"] || !sources["project"] {
		t.Errorf("expected both user and project sources, got %+v", listResp.Data.Skills)
	}
}

// =============================================
// Skills Load — Partial ID Tests
// =============================================

func TestAgentSkillList_OnlyUserIDProvided(t *testing.T) {
	r, _, udMgr, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "user-s", "---\nname: user-s\n---\nuser content")
	writeLayerSkill(t, pdMgr.Root("p1"), "proj-s", "---\nname: proj-s\n---\nproject content")

	body := `{"skill_ids":[],"user_id":"u1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 1 {
		t.Fatalf("expected 1 user skill only, got %d", got)
	}
	if listResp.Data.Skills[0].Name != "user-s" {
		t.Errorf("expected user-s, got %s", listResp.Data.Skills[0].Name)
	}
}

func TestAgentSkillList_OnlyProjectIDProvided(t *testing.T) {
	r, _, udMgr, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "user-s", "---\nname: user-s\n---\nuser content")
	writeLayerSkill(t, pdMgr.Root("p1"), "proj-s", "---\nname: proj-s\n---\nproject content")

	body := `{"skill_ids":[],"project_id":"p1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 1 {
		t.Fatalf("expected 1 project skill only, got %d", got)
	}
	if listResp.Data.Skills[0].Name != "proj-s" {
		t.Errorf("expected proj-s, got %s", listResp.Data.Skills[0].Name)
	}
}

// =============================================
// Skills Load — Frontmatter Handling for Layer Skills
// =============================================

func TestAgentSkillList_UserProjectFrontmatterExtraction(t *testing.T) {
	r, _, udMgr, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "fm-skill",
		"---\nname: fm-skill\ndescription: \"A skill\"\ntags: [x, y]\n---\n## Body\nContent here.")
	writeLayerSkill(t, pdMgr.Root("p1"), "pf-skill",
		"---\nname: pf-skill\nversion: \"1.0\"\n---\n## Project Body\nMore content.")

	body := `{"skill_ids":[],"user_id":"u1","project_id":"p1"}`

	// List — frontmatter fields without --- delimiters
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	byName := make(map[string]model.SkillSummary)
	for _, s := range listResp.Data.Skills {
		byName[s.Name] = s
	}
	// User skill frontmatter
	if !strings.Contains(byName["fm-skill"].Frontmatter, "tags:") {
		t.Errorf("expected frontmatter to contain 'tags:', got %q", byName["fm-skill"].Frontmatter)
	}
	if strings.Contains(byName["fm-skill"].Frontmatter, "## Body") {
		t.Error("frontmatter should not contain body content")
	}
	if strings.Contains(byName["fm-skill"].Frontmatter, "---") {
		t.Error("frontmatter should not contain --- delimiters")
	}
	// Project skill frontmatter
	if !strings.Contains(byName["pf-skill"].Frontmatter, "version:") {
		t.Errorf("expected frontmatter to contain 'version:', got %q", byName["pf-skill"].Frontmatter)
	}

	// Load — body only, no frontmatter
	w = doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/load", body)
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	loaded := make(map[string]model.SkillContent)
	for _, s := range loadResp.Data.Skills {
		loaded[s.Name] = s
	}
	if !strings.Contains(loaded["fm-skill"].Content, "## Body") {
		t.Errorf("load content should contain body, got %q", loaded["fm-skill"].Content)
	}
	if strings.Contains(loaded["fm-skill"].Content, "tags:") {
		t.Error("load content should not contain frontmatter fields")
	}
}

func TestAgentSkillList_SkillWithoutFrontmatter(t *testing.T) {
	r, _, udMgr, _ := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "plain-skill", "No frontmatter here, just plain content.")

	body := `{"skill_ids":[],"user_id":"u1"}`
	// List — frontmatter should be empty
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if listResp.Data.Skills[0].Frontmatter != "" {
		t.Errorf("expected empty frontmatter, got %q", listResp.Data.Skills[0].Frontmatter)
	}

	// Load — content should be the full text
	w = doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/load", body)
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loadResp.Data.Skills[0].Content != "No frontmatter here, just plain content." {
		t.Errorf("expected full text as content, got %q", loadResp.Data.Skills[0].Content)
	}
}

// =============================================
// Skills Load — scanLayerSkills Edge Cases
// =============================================

func TestAgentSkillList_SkillDirWithoutSkillsMD(t *testing.T) {
	r, _, udMgr, _ := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "valid-skill", "---\nname: valid\n---\nvalid body")
	// Create a directory without SKILLS.md
	invalidDir := filepath.Join(udMgr.Root("u1"), "skills", "no-md-skill")
	if err := os.MkdirAll(invalidDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	os.WriteFile(filepath.Join(invalidDir, "README.md"), []byte("not a skill"), 0644)

	body := `{"skill_ids":[],"user_id":"u1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 1 {
		t.Fatalf("expected 1 valid skill (no-md-skill should be skipped), got %d", got)
	}
	if listResp.Data.Skills[0].Name != "valid-skill" {
		t.Errorf("expected valid-skill, got %s", listResp.Data.Skills[0].Name)
	}
}

// TestAgentSkillList_SkillMDVariants verifies that scanLayerSkills discovers
// skills using any accepted filename variant (SKILLS.md, SKILLS.MD, SKILL.md).
func TestAgentSkillList_SkillMDVariants(t *testing.T) {
	r, _, _, pdMgr := setupSkillRouterWithLayers()

	// Create skills with different filename variants
	for _, tc := range []struct {
		name     string
		filename string
	}{
		{"skill-upper", "SKILLS.md"},
		{"skill-allupper", "SKILLS.MD"},
		{"skill-singular", "SKILL.md"},
	} {
		dir := filepath.Join(pdMgr.Root("p1"), "skills", tc.name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", tc.name, err)
		}
		content := fmt.Sprintf("---\nname: %s\n---\nbody of %s", tc.name, tc.name)
		if err := os.WriteFile(filepath.Join(dir, tc.filename), []byte(content), 0644); err != nil {
			t.Fatalf("write %s/%s: %v", tc.name, tc.filename, err)
		}
	}

	body := `{"skill_ids":[],"project_id":"p1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 3 {
		t.Fatalf("expected 3 skills (all filename variants), got %d", got)
	}
	found := map[string]bool{}
	for _, s := range listResp.Data.Skills {
		found[s.Name] = true
		if s.Source != "project" {
			t.Errorf("skill %s: expected source=project, got %s", s.Name, s.Source)
		}
	}
	for _, name := range []string{"skill-upper", "skill-allupper", "skill-singular"} {
		if !found[name] {
			t.Errorf("skill %s not found in list results", name)
		}
	}
}

func TestAgentSkillList_NonexistentUserProjectDirs(t *testing.T) {
	r, _, _, _ := setupSkillRouterWithLayers()

	body := `{"skill_ids":[],"user_id":"no-such-user","project_id":"no-such-project"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for nonexistent dirs, got %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 0 {
		t.Fatalf("expected 0 skills for nonexistent dirs, got %d", got)
	}
}

// =============================================
// Skills Load — Agent Cache Interaction with Layer Override
// =============================================

func TestAgentSkillList_AgentCacheNotPopulatedForOverriddenSkill(t *testing.T) {
	r, mgr, udMgr, _ := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "shared", "---\nname: shared\n---\nuser shared")
	writeGlobalSkill(t, mgr, "shared", "---\nname: shared\n---\nagent shared")

	body := `{"skill_ids":["shared"],"user_id":"u1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/load", body)
	if w.Code != http.StatusOK {
		t.Fatalf("load failed: %d %s", w.Code, w.Body.String())
	}
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(loadResp.Data.Skills); got != 1 {
		t.Fatalf("expected 1 skill, got %d", got)
	}
	if loadResp.Data.Skills[0].Source != "user" {
		t.Fatalf("expected source=user, got %s", loadResp.Data.Skills[0].Source)
	}

	// Agent cache for "shared" should NOT exist (syncSkillToAgent was skipped due to seen["shared"])
	agentCacheDir := filepath.Join(mgr.SkillsRoot("a1"), "shared")
	if _, err := os.Stat(agentCacheDir); err == nil {
		t.Error("agent cache for 'shared' should NOT exist because user layer overrides it")
	}
}

// =============================================
// Skills Load — Cleanup with Layer Skills Present
// =============================================

func TestAgentSkillCleanup_WithUserProjectSkillsPresent(t *testing.T) {
	r, mgr, udMgr, pdMgr := setupSkillRouterWithLayers()

	writeLayerSkill(t, udMgr.Root("u1"), "user-s", "---\nname: user-s\n---\nuser body")
	writeLayerSkill(t, pdMgr.Root("p1"), "proj-s", "---\nname: proj-s\n---\nproject body")
	writeGlobalSkill(t, mgr, "agent-keep", "---\nname: agent-keep\n---\nkeep body")
	writeGlobalSkill(t, mgr, "agent-remove", "---\nname: agent-remove\n---\nremove body")

	// Load all four
	body := `{"skill_ids":["agent-keep","agent-remove"],"user_id":"u1","project_id":"p1"}`
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/cleanup-a/load", body)
	if w.Code != http.StatusOK {
		t.Fatalf("load failed: %d %s", w.Code, w.Body.String())
	}

	// Verify both agent skills cached
	for _, name := range []string{"agent-keep", "agent-remove"} {
		if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("cleanup-a"), name)); err != nil {
			t.Fatalf("agent skill %s should be cached: %v", name, err)
		}
	}

	// Load with cleanup, keeping only agent-keep
	cleanupBody := `{"skill_ids":["agent-keep"],"user_id":"u1","project_id":"p1","cleanup":true}`
	w = doRequest(t, r, http.MethodPost, "/v1/skills/agents/cleanup-a/load", cleanupBody)
	if w.Code != http.StatusOK {
		t.Fatalf("cleanup load failed: %d %s", w.Code, w.Body.String())
	}

	// agent-remove should be gone from cache
	if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("cleanup-a"), "agent-remove")); err == nil {
		t.Error("agent-remove should have been cleaned up")
	}
	// agent-keep should remain
	if _, err := os.Stat(filepath.Join(mgr.SkillsRoot("cleanup-a"), "agent-keep")); err != nil {
		t.Errorf("agent-keep should still be cached: %v", err)
	}
	// User and project skill dirs should be untouched
	if _, err := os.Stat(filepath.Join(udMgr.Root("u1"), "skills", "user-s", "SKILLS.md")); err != nil {
		t.Errorf("user skill should be untouched: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pdMgr.Root("p1"), "skills", "proj-s", "SKILLS.md")); err != nil {
		t.Errorf("project skill should be untouched: %v", err)
	}

	// Response should have 3 skills (user + project + kept agent)
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(loadResp.Data.Skills); got != 3 {
		t.Fatalf("expected 3 skills after cleanup, got %d: %+v", got, loadResp.Data.Skills)
	}
}

// =============================================
// Skills Load — Additional Files in Skill Directory
// =============================================

func TestAgentSkillList_SkillWithAdditionalFiles(t *testing.T) {
	r, _, udMgr, _ := setupSkillRouterWithLayers()

	// Create user skill with extra files
	skillDir := filepath.Join(udMgr.Root("u1"), "skills", "multi-file")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte("---\nname: multi-file\n---\nmain content"), 0644)
	os.WriteFile(filepath.Join(skillDir, "helper.py"), []byte("print('hi')"), 0644)
	os.MkdirAll(filepath.Join(skillDir, "data"), 0755)
	os.WriteFile(filepath.Join(skillDir, "data", "config.json"), []byte(`{"k":"v"}`), 0644)

	body := `{"skill_ids":[],"user_id":"u1"}`
	// List
	w := doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/list", body)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}
	var listResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := len(listResp.Data.Skills); got != 1 {
		t.Fatalf("expected 1 skill, got %d", got)
	}
	// Frontmatter should come only from SKILLS.md
	if !strings.Contains(listResp.Data.Skills[0].Frontmatter, "name: multi-file") {
		t.Errorf("frontmatter missing name, got %q", listResp.Data.Skills[0].Frontmatter)
	}

	// Load — content should be SKILLS.md body only
	w = doRequest(t, r, http.MethodPost, "/v1/skills/agents/a1/load", body)
	var loadResp struct {
		Success bool                       `json:"success"`
		Data    model.AgentSkillLoadResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &loadResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if loadResp.Data.Skills[0].Content != "main content" {
		t.Errorf("expected 'main content', got %q", loadResp.Data.Skills[0].Content)
	}
}
