package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
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
	skillH.SetSSRFProtection(false) // disable SSRF for tests using httptest (loopback)
	skills := r.Group("/v1/skills")
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

func TestSkillCreate(t *testing.T) {
	r, mgr := setupSkillRouter()

	body := `{"name": "my-skill", "description": "A test skill"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skill := data["skill"].(map[string]interface{})
	if skill["name"] != "my-skill" {
		t.Errorf("expected name 'my-skill', got %v", skill["name"])
	}
	if skill["description"] != "A test skill" {
		t.Errorf("expected description 'A test skill', got %v", skill["description"])
	}

	// Verify directory exists
	skillDir := mgr.GlobalSkillPath("my-skill")
	if _, err := os.Stat(skillDir); err != nil {
		t.Errorf("skill directory not created: %v", err)
	}
	// Verify _meta.json exists
	if _, err := os.Stat(filepath.Join(skillDir, "_meta.json")); err != nil {
		t.Errorf("_meta.json not created: %v", err)
	}
	// Verify SKILLS.md exists
	if _, err := os.Stat(filepath.Join(skillDir, "SKILLS.md")); err != nil {
		t.Errorf("SKILLS.md not created: %v", err)
	}
}

func TestSkillCreateDuplicate(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "dup-skill", "description": "first"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first create failed: %d", w.Code)
	}

	// Second create should fail with 409
	req2 := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Errorf("expected 409 Conflict, got %d", w2.Code)
	}
}

func TestSkillCreateInvalidID(t *testing.T) {
	r, _ := setupSkillRouter()

	tests := []struct {
		name string
		id   string
	}{
		{"spaces", "my skill"},
		{"underscores", "my_skill"},
		{"dots", "my.skill"},
		{"special chars", "my@skill!"},
		{"empty", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"name": "` + tt.id + `"}`
			req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for ID %q, got %d", tt.id, w.Code)
			}
		})
	}
}

func TestSkillImport(t *testing.T) {
	r, mgr := setupSkillRouter()

	zipPath := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: imported-skill\ndescription: Imported from zip\n---\nContent here.",
		"script.sh": "echo hello",
	})
	defer os.Remove(zipPath)

	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipPath)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	body := `{"name": "imported-skill", "zip_url": "` + server.URL + `/skill.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skill := data["skill"].(map[string]interface{})
	if skill["name"] != "imported-skill" {
		t.Errorf("expected name 'imported-skill', got %v", skill["name"])
	}

	// Verify files were extracted
	skillDir := mgr.GlobalSkillPath("imported-skill")
	if _, err := os.Stat(filepath.Join(skillDir, "SKILLS.MD")); err != nil {
		t.Errorf("SKILLS.MD not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "script.sh")); err != nil {
		t.Errorf("script.sh not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "_meta.json")); err != nil {
		t.Errorf("_meta.json not created: %v", err)
	}
}

func TestSkillImportSSRF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("sandbox-skill-ssrf-%d-%d", time.Now().UnixNano(), os.Getpid()))
	os.MkdirAll(dir, 0755)
	globalSkillsDir := filepath.Join(dir, "global-skills")
	os.MkdirAll(globalSkillsDir, 0755)
	mgr := session.NewManager(dir, 24*time.Hour)
	mgr.SetGlobalSkillsRoot(globalSkillsDir)

	r := gin.New()
	skillH := NewSkillHandler(mgr)
	// SSRF protection enabled (default)
	r.POST("/v1/skills/import", skillH.Import)

	body := `{"name": "ssrf-test", "zip_url": "http://127.0.0.1/skill.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (SSRF blocked), got %d %s", w.Code, w.Body.String())
	}
}

func TestSkillListGlobal(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create two skills
	for _, name := range []string{"skill-a", "skill-b"} {
		body := `{"name": "` + name + `", "description": "test"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	// List
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(`{}`))
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
	if len(skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(skills))
	}
}

func TestSkillDelete(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create a skill
	body := `{"name": "to-delete", "description": "will be deleted"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create failed: %d", w.Code)
	}

	// Delete
	delBody := `{"name": "to-delete"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
	}

	// Verify directory removed
	if _, err := os.Stat(mgr.GlobalSkillPath("to-delete")); err == nil {
		t.Error("skill directory should have been removed")
	}
}

func TestSkillTree(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create a skill with subdirectories
	body := `{"name": "tree-skill", "description": "test tree"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write a file
	writeBody := `{"name": "tree-skill", "path": "src/main.py", "content": "print('hello')"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Get tree
	treeBody := `{"name": "tree-skill"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/tree", bytes.NewBufferString(treeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("tree failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})

	// Should have: src/ (dir), src/main.py (file), SKILLS.md (file), _meta.json (file)
	if len(files) < 3 {
		t.Errorf("expected at least 3 entries, got %d", len(files))
	}
}

func TestSkillFileReadWrite(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill
	body := `{"name": "rw-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write file
	writeBody := `{"name": "rw-skill", "path": "test.txt", "content": "hello world"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write failed: %d %s", w.Code, w.Body.String())
	}

	// Read file
	readBody := `{"name": "rw-skill", "path": "test.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/read", bytes.NewBufferString(readBody))
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
		t.Errorf("expected content 'hello world', got %v", data["content"])
	}
}

func TestSkillFileWriteUpdatesMeta(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create skill
	body := `{"name": "meta-test", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read initial meta
	skillDir := mgr.GlobalSkillPath("meta-test")
	initialMeta, _ := readSkillMeta(skillDir)
	initialUpdatedAt := initialMeta.UpdatedAt

	// Write a file (triggers touchSkillMeta which updates updated_at)
	writeBody := `{"name": "meta-test", "path": "newfile.txt", "content": "content"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read updated meta
	updatedMeta, _ := readSkillMeta(skillDir)
	if updatedMeta.UpdatedAt <= initialUpdatedAt {
		t.Errorf("expected updated_at to increase, initial=%d, updated=%d", initialUpdatedAt, updatedMeta.UpdatedAt)
	}
}

func TestSkillFileUpdate(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill
	body := `{"name": "update-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write file
	writeBody := `{"name": "update-skill", "path": "replace.txt", "content": "foo bar foo baz"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Update (replace)
	updateBody := `{"name": "update-skill", "path": "replace.txt", "old_str": "foo", "new_str": "qux"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/update", bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if int(data["replaced_count"].(float64)) != 2 {
		t.Errorf("expected 2 replacements, got %v", data["replaced_count"])
	}

	// Verify content
	readBody := `{"name": "update-skill", "path": "replace.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/read", bytes.NewBufferString(readBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &resp)
	data = resp["data"].(map[string]interface{})
	if data["content"] != "qux bar qux baz" {
		t.Errorf("expected 'qux bar qux baz', got %v", data["content"])
	}
}

func TestSkillFileMkdir(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill
	body := `{"name": "mkdir-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Create directory
	mkdirBody := `{"name": "mkdir-skill", "path": "src/utils"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/mkdir", bytes.NewBufferString(mkdirBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("mkdir failed: %d %s", w.Code, w.Body.String())
	}

	// Write a file into the new directory
	writeBody := `{"name": "mkdir-skill", "path": "src/utils/helper.py", "content": "def help(): pass"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write to mkdir dir failed: %d %s", w.Code, w.Body.String())
	}
}

func TestSkillFilePathTraversal(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill
	body := `{"name": "traversal-test", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	tests := []struct {
		name string
		path string
	}{
		{"parent traversal", "../etc/passwd"},
		{"deep traversal", "a/../../etc/passwd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readBody := `{"name": "traversal-test", "path": "` + tt.path + `"}`
			req := httptest.NewRequest(http.MethodPost, "/v1/skills/file/read", bytes.NewBufferString(readBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400 for path %q, got %d", tt.path, w.Code)
			}
		})
	}
}

func TestAgentSkillLoad(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create global skill with specific SKILLS.md content
	body := `{"name": "load-skill", "description": "test load"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Load into agent via new route
	loadBody := `{"skill_ids": ["load-skill"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
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
	r, _ := setupSkillRouter()

	// Create global skill
	body := `{"name": "list-skill", "description": "test list"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// List via new route
	listBody := `{"skill_ids": ["list-skill"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/list", bytes.NewBufferString(listBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
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
	r, _ := setupSkillRouter()

	// Create global skill
	body := `{"name": "cache-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// First load
	loadBody := `{"skill_ids": ["cache-skill"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
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

func TestAgentSkillLoadCacheInvalidation(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create global skill
	body := `{"name": "inv-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Load into agent
	loadBody := `{"skill_ids": ["inv-skill"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first load failed: %d", w.Code)
	}

	// Modify global skill (write a file triggers touchSkillMeta)
	writeBody := `{"name": "inv-skill", "path": "new-data.txt", "content": "updated content"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Verify global meta was updated
	globalMeta, _ := readSkillMeta(mgr.GlobalSkillPath("inv-skill"))
	if globalMeta.UpdatedAt == 0 {
		t.Error("expected updated_at to be set")
	}

	// Load again - should re-copy due to cache invalidation
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/a1/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("second load failed: %d %s", w.Code, w.Body.String())
	}

	// Verify the new file exists in agent cache
	agentFile := filepath.Join(mgr.SkillsRoot("a1"), "inv-skill", "new-data.txt")
	if _, err := os.Stat(agentFile); err != nil {
		t.Errorf("new file not copied to agent cache: %v", err)
	}
}

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

func TestAgentSkillLoadImportedFrontmatterSplit(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create a skill with rich frontmatter via import
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
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import", bytes.NewBufferString(importBody))
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

func TestSkillDeleteNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/delete", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSkillTreeNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/tree", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// =============================================
// Tests for code review fixes
// =============================================

func TestSkillFileWriteMetaProtectionCaseInsensitive(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill
	body := `{"name": "meta-protect", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Try to write _meta.json directly
	writeBody := `{"name": "meta-protect", "path": "_meta.json", "content": "hacked"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for _meta.json write, got %d", w.Code)
	}

	// Try case variation _META.JSON
	writeBody2 := `{"name": "meta-protect", "path": "_META.JSON", "content": "hacked"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody2))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for _META.JSON write, got %d", w.Code)
	}

	// Try to update _meta.json
	updateBody := `{"name": "meta-protect", "path": "_meta.json", "old_str": "a", "new_str": "b"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/update", bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for _meta.json update, got %d", w.Code)
	}
}

func TestSkillFileUpdateNoOpSkipsWrite(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create skill and write a file
	body := `{"name": "noop-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	writeBody := `{"name": "noop-skill", "path": "test.txt", "content": "hello world"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Record meta timestamp after write
	skillDir := mgr.GlobalSkillPath("noop-skill")
	metaAfterWrite, _ := readSkillMeta(skillDir)
	tsAfterWrite := metaAfterWrite.UpdatedAt

	// Update with a string that doesn't exist — should be a no-op
	updateBody := `{"name": "noop-skill", "path": "test.txt", "old_str": "NOTFOUND", "new_str": "replacement"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/update", bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if int(data["replaced_count"].(float64)) != 0 {
		t.Errorf("expected 0 replacements, got %v", data["replaced_count"])
	}

	// Meta timestamp should NOT have changed
	metaAfterNoop, _ := readSkillMeta(skillDir)
	if metaAfterNoop.UpdatedAt != tsAfterWrite {
		t.Errorf("expected meta timestamp unchanged after no-op update, before=%d after=%d", tsAfterWrite, metaAfterNoop.UpdatedAt)
	}
}

func TestSkillFileWriteEmptyContent(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill
	body := `{"name": "empty-write", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write empty file (should succeed now that binding:"required" is removed from Content)
	writeBody := `{"name": "empty-write", "path": "empty.txt", "content": ""}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty content write, got %d %s", w.Code, w.Body.String())
	}

	// Read it back
	readBody := `{"name": "empty-write", "path": "empty.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/read", bytes.NewBufferString(readBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("read failed: %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "" {
		t.Errorf("expected empty content, got %q", data["content"])
	}
}

func TestSkillCreateYAMLInjectionSafe(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create skill with description containing YAML-breaking characters
	body := `{"name": "yaml-test", "description": "line1\n---\nevil: true"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}

	// Read SKILLS.md and verify description is quoted
	skillDir := mgr.GlobalSkillPath("yaml-test")
	content, err := os.ReadFile(filepath.Join(skillDir, "SKILLS.md"))
	if err != nil {
		t.Fatalf("failed to read SKILLS.md: %v", err)
	}

	// The description should be quoted to prevent YAML injection
	if !bytes.Contains(content, []byte(`description: "`)) {
		t.Errorf("expected quoted description in SKILLS.md, got:\n%s", string(content))
	}
}

func TestExtractZipNestedDirsWithoutDirFlag(t *testing.T) {
	// Simulate zip files where directory entries lack the proper directory flag
	// but are indicated by a trailing "/" in the name (common on Windows zip tools).
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// Create directory entry WITHOUT setting ModeDir — only the trailing "/" identifies it
	fh := &zip.FileHeader{Name: "scripts/", Method: zip.Deflate}
	fh.SetMode(0644) // regular file mode, NOT a directory
	if _, err := w.CreateHeader(fh); err != nil {
		t.Fatalf("CreateHeader scripts/: %v", err)
	}

	// File inside that directory
	f, err := w.Create("scripts/run_bioinfor.py")
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
	// Normal file
	f, _ := w.Create("normal.txt")
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

func TestValidateSkillURL(t *testing.T) {
	tests := []struct {
		name        string
		ssrfEnabled bool
		rawURL      string
		wantErr     bool
	}{
		{"loopback blocked when SSRF on", true, "http://127.0.0.1/metadata", true},
		{"private 10.x blocked when SSRF on", true, "http://10.0.0.1/internal", true},
		{"private 192.168.x blocked when SSRF on", true, "http://192.168.1.1/internal", true},
		{"private 172.16.x blocked when SSRF on", true, "http://172.16.0.1/internal", true},
		{"link-local blocked when SSRF on", true, "http://169.254.169.254/metadata", true},
		{"file scheme blocked when SSRF on", true, "file:///etc/passwd", true},
		{"ftp scheme blocked when SSRF on", true, "ftp://example.com/file", true},
		{"empty host blocked when SSRF on", true, "http://", true},
		{"SSRF off allows loopback", false, "http://127.0.0.1/metadata", false},
		{"SSRF off allows file scheme", false, "file:///etc/passwd", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &SkillHandler{ssrfProtection: tt.ssrfEnabled}
			err := h.validateSkillURL(tt.rawURL)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSkillURL(%q) = %v, wantErr %v", tt.rawURL, err, tt.wantErr)
			}
		})
	}
}

// =============================================
// Tests for file/delete endpoint
// =============================================

func TestSkillFileDeleteFile(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill and write a file
	body := `{"name": "del-file-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	writeBody := `{"name": "del-file-skill", "path": "to-remove.txt", "content": "bye"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Delete the file
	delBody := `{"name": "del-file-skill", "path": "to-remove.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete file failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["path"] != "to-remove.txt" {
		t.Errorf("expected path 'to-remove.txt', got %v", data["path"])
	}

	// Verify file is gone
	readBody := `{"name": "del-file-skill", "path": "to-remove.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/read", bytes.NewBufferString(readBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 after delete, got %d", w.Code)
	}
}

func TestSkillFileDeleteDirectory(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill with nested files
	body := `{"name": "del-dir-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	for _, path := range []string{"src/a.py", "src/sub/b.py"} {
		writeBody := `{"name": "del-dir-skill", "path": "` + path + `", "content": "x"}`
		req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	// Delete the entire src/ directory
	delBody := `{"name": "del-dir-skill", "path": "src"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete dir failed: %d %s", w.Code, w.Body.String())
	}

	// Verify nested files are gone
	readBody := `{"name": "del-dir-skill", "path": "src/a.py"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/read", bytes.NewBufferString(readBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for deleted nested file, got %d", w.Code)
	}
}

func TestSkillFileDeleteMetaBlocked(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "del-meta-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	delBody := `{"name": "del-meta-skill", "path": "_meta.json"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for _meta.json delete, got %d", w.Code)
	}
}

func TestSkillFileDeletePathTraversal(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "del-traversal", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	delBody := `{"name": "del-traversal", "path": "../other-skill"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal, got %d", w.Code)
	}
}

func TestSkillFileDeleteNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "del-nf-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	delBody := `{"name": "del-nf-skill", "path": "nonexistent.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent path, got %d", w.Code)
	}
}

// =============================================
// Tests for import/upload endpoint
// =============================================

func TestSkillImportUpload(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create a test ZIP in memory
	zipBuf := createTestZipBytes(t, map[string]string{
		"SKILLS.md": "---\nname: uploaded-skill\ndescription: From upload\n---\nBody content.",
		"main.py":   "print('hello')",
	})

	// Build multipart form
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("names", "uploaded-skill")
	part, err := writer.CreateFormFile("files", "skill.zip")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(zipBuf)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload import failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skills := data["skills"].([]interface{})
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}
	skill := skills[0].(map[string]interface{})
	if skill["name"] != "uploaded-skill" {
		t.Errorf("expected name 'uploaded-skill', got %v", skill["name"])
	}
	if skill["description"] != "From upload" {
		t.Errorf("expected description 'From upload', got %v", skill["description"])
	}

	// Verify files extracted
	skillDir := mgr.GlobalSkillPath("uploaded-skill")
	if _, err := os.Stat(filepath.Join(skillDir, "main.py")); err != nil {
		t.Errorf("main.py not extracted: %v", err)
	}
}

func TestSkillImportUploadMultiple(t *testing.T) {
	r, mgr := setupSkillRouter()

	zip1 := createTestZipBytes(t, map[string]string{
		"SKILLS.md": "---\nname: skill-one\n---\nFirst skill.",
	})
	zip2 := createTestZipBytes(t, map[string]string{
		"SKILLS.md": "---\nname: skill-two\n---\nSecond skill.",
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("names", "skill-one")
	writer.WriteField("names", "skill-two")

	p1, _ := writer.CreateFormFile("files", "one.zip")
	p1.Write(zip1)
	p2, _ := writer.CreateFormFile("files", "two.zip")
	p2.Write(zip2)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("multi upload failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skills := data["skills"].([]interface{})
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}

	// Verify both skill directories exist
	for _, name := range []string{"skill-one", "skill-two"} {
		if _, err := os.Stat(mgr.GlobalSkillPath(name)); err != nil {
			t.Errorf("skill %s directory not created: %v", name, err)
		}
	}
}

func TestSkillImportUploadNamesMismatch(t *testing.T) {
	r, _ := setupSkillRouter()

	zip1 := createTestZipBytes(t, map[string]string{"SKILLS.md": "---\nname: x\n---\n"})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// 1 file but 2 names
	writer.WriteField("names", "skill-a")
	writer.WriteField("names", "skill-b")
	p, _ := writer.CreateFormFile("files", "one.zip")
	p.Write(zip1)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for names/files mismatch, got %d", w.Code)
	}
}

func TestSkillImportUploadNoFiles(t *testing.T) {
	r, _ := setupSkillRouter()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for no files, got %d", w.Code)
	}
}

func TestSkillImportUploadDuplicateNames(t *testing.T) {
	r, _ := setupSkillRouter()

	zip1 := createTestZipBytes(t, map[string]string{"SKILLS.md": "---\nname: dup\n---\n"})
	zip2 := createTestZipBytes(t, map[string]string{"SKILLS.md": "---\nname: dup\n---\n"})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("names", "dup-skill")
	writer.WriteField("names", "dup-skill")

	p1, _ := writer.CreateFormFile("files", "one.zip")
	p1.Write(zip1)
	p2, _ := writer.CreateFormFile("files", "two.zip")
	p2.Write(zip2)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for duplicate names, got %d %s", w.Code, w.Body.String())
	}
}

func TestSkillFileDeleteRootBlocked(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "del-root-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Try to delete "." which resolves to skill root
	delBody := `{"name": "del-root-skill", "path": "."}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for root delete, got %d %s", w.Code, w.Body.String())
	}
}

func TestSkillFileDeleteMetaCaseInsensitive(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "del-meta-ci", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Try case variation _META.JSON
	delBody := `{"name": "del-meta-ci", "path": "_META.JSON"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for _META.JSON delete, got %d", w.Code)
	}
}

func TestSkillFileDeleteUpdatesMeta(t *testing.T) {
	r, mgr := setupSkillRouter()

	body := `{"name": "del-meta-ts", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write a file
	writeBody := `{"name": "del-meta-ts", "path": "tmp.txt", "content": "x"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	skillDir := mgr.GlobalSkillPath("del-meta-ts")
	metaBefore, _ := readSkillMeta(skillDir)

	// Delete the file
	delBody := `{"name": "del-meta-ts", "path": "tmp.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/delete", bytes.NewBufferString(delBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d", w.Code)
	}

	metaAfter, _ := readSkillMeta(skillDir)
	if metaAfter.UpdatedAt <= metaBefore.UpdatedAt {
		t.Errorf("expected updated_at to increase after delete, before=%d after=%d", metaBefore.UpdatedAt, metaAfter.UpdatedAt)
	}
}

func TestSkillImportUploadPreservesCreatedAt(t *testing.T) {
	r, mgr := setupSkillRouter()

	// First create the skill
	body := `{"name": "upload-preserve", "description": "original"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	skillDir := mgr.GlobalSkillPath("upload-preserve")
	originalMeta, _ := readSkillMeta(skillDir)
	originalCreatedAt := originalMeta.CreatedAt

	// Re-import via upload
	zipBuf := createTestZipBytes(t, map[string]string{
		"SKILLS.md": "---\nname: upload-preserve\ndescription: updated\n---\nNew body.",
	})

	var mbody bytes.Buffer
	writer := multipart.NewWriter(&mbody)
	writer.WriteField("names", "upload-preserve")
	part, _ := writer.CreateFormFile("files", "skill.zip")
	part.Write(zipBuf)
	writer.Close()

	req = httptest.NewRequest(http.MethodPost, "/v1/skills/import/upload", &mbody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload re-import failed: %d %s", w.Code, w.Body.String())
	}

	updatedMeta, _ := readSkillMeta(skillDir)
	if updatedMeta.CreatedAt != originalCreatedAt {
		t.Errorf("expected created_at preserved (%d), got %d", originalCreatedAt, updatedMeta.CreatedAt)
	}
	if updatedMeta.UpdatedAt <= originalCreatedAt {
		t.Errorf("expected updated_at > created_at")
	}
}

func TestSkillImportUploadTooManyFiles(t *testing.T) {
	r, _ := setupSkillRouter()

	zipBuf := createTestZipBytes(t, map[string]string{"SKILLS.md": "---\nname: x\n---\n"})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i := 0; i < maxUploadFiles+1; i++ {
		name := fmt.Sprintf("skill-%d", i)
		writer.WriteField("names", name)
		p, _ := writer.CreateFormFile("files", name+".zip")
		p.Write(zipBuf)
	}
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for too many files, got %d %s", w.Code, w.Body.String())
	}
}

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
// Tests for new skill APIs
// =============================================

func TestSkillGet(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill
	body := `{"name": "get-skill", "description": "A test skill"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write some body content to SKILLS.md
	writeBody := `{"name": "get-skill", "path": "SKILLS.md", "content": "---\nname: get-skill\ndescription: \"A test skill\"\n---\n## Usage\nDo stuff."}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Get skill
	getBody := `{"name": "get-skill"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/get", bytes.NewBufferString(getBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skill := data["skill"].(map[string]interface{})
	if skill["name"] != "get-skill" {
		t.Errorf("expected name 'get-skill', got %v", skill["name"])
	}
	if skill["description"] != "A test skill" {
		t.Errorf("expected description 'A test skill', got %v", skill["description"])
	}
	fm := data["frontmatter"].(string)
	if !strings.Contains(fm, "name: get-skill") {
		t.Errorf("expected frontmatter to contain 'name: get-skill', got %q", fm)
	}
	body2 := data["body"].(string)
	if !strings.Contains(body2, "## Usage") {
		t.Errorf("expected body to contain '## Usage', got %q", body2)
	}
}

func TestSkillGetNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "nonexistent"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/get", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSkillUpdate(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create skill
	body := `{"name": "upd-skill", "description": "original"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Update description
	updBody := `{"name": "upd-skill", "description": "updated desc"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/update", bytes.NewBufferString(updBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skill := data["skill"].(map[string]interface{})
	if skill["description"] != "updated desc" {
		t.Errorf("expected description 'updated desc', got %v", skill["description"])
	}

	// Verify _meta.json was updated
	skillDir := mgr.GlobalSkillPath("upd-skill")
	meta, _ := readSkillMeta(skillDir)
	if meta.Description != "updated desc" {
		t.Errorf("meta description not updated: %s", meta.Description)
	}

	// Verify SKILLS.md frontmatter was updated
	content, _ := os.ReadFile(filepath.Join(skillDir, "SKILLS.md"))
	if !strings.Contains(string(content), `description: "updated desc"`) {
		t.Errorf("SKILLS.md frontmatter not updated: %s", string(content))
	}
}

func TestSkillUpdateNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "nonexistent", "description": "x"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/update", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSkillRename(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create skill
	body := `{"name": "old-name", "description": "rename test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Rename
	renBody := `{"name": "old-name", "new_name": "new-name"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/rename", bytes.NewBufferString(renBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("rename failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skill := data["skill"].(map[string]interface{})
	if skill["name"] != "new-name" {
		t.Errorf("expected name 'new-name', got %v", skill["name"])
	}

	// Old dir should not exist
	if _, err := os.Stat(mgr.GlobalSkillPath("old-name")); err == nil {
		t.Error("old skill directory should not exist")
	}
	// New dir should exist
	if _, err := os.Stat(mgr.GlobalSkillPath("new-name")); err != nil {
		t.Errorf("new skill directory should exist: %v", err)
	}

	// Verify SKILLS.md frontmatter has new name
	content, _ := os.ReadFile(filepath.Join(mgr.GlobalSkillPath("new-name"), "SKILLS.md"))
	if !strings.Contains(string(content), "name: new-name") {
		t.Errorf("SKILLS.md frontmatter not updated: %s", string(content))
	}
}

func TestSkillRenameSameName(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "same-skill", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	renBody := `{"name": "same-skill", "new_name": "same-skill"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/rename", bytes.NewBufferString(renBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for same name rename, got %d", w.Code)
	}
}

func TestSkillRenameTargetExists(t *testing.T) {
	r, _ := setupSkillRouter()

	for _, name := range []string{"src-skill", "dst-skill"} {
		body := `{"name": "` + name + `", "description": "test"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	renBody := `{"name": "src-skill", "new_name": "dst-skill"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/rename", bytes.NewBufferString(renBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for existing target, got %d", w.Code)
	}
}

func TestSkillExport(t *testing.T) {
	r, _ := setupSkillRouter()

	// Create skill with files
	body := `{"name": "export-skill", "description": "export test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	writeBody := `{"name": "export-skill", "path": "script.py", "content": "print('hi')"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Export
	req = httptest.NewRequest(http.MethodGet, "/v1/skills/export?name=export-skill", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("expected Content-Type application/zip, got %s", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "export-skill.zip") {
		t.Errorf("expected Content-Disposition with export-skill.zip, got %s", cd)
	}

	// Verify ZIP contents
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("failed to read ZIP: %v", err)
	}

	fileNames := make(map[string]bool)
	for _, f := range zr.File {
		fileNames[f.Name] = true
	}

	if !fileNames["SKILLS.md"] {
		t.Error("ZIP should contain SKILLS.md")
	}
	if !fileNames["script.py"] {
		t.Error("ZIP should contain script.py")
	}
	if fileNames["_meta.json"] {
		t.Error("ZIP should NOT contain _meta.json")
	}
}

func TestSkillExportNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	req := httptest.NewRequest(http.MethodGet, "/v1/skills/export?name=nonexistent", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSkillCopy(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create skill with files
	body := `{"name": "copy-src", "description": "copy test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	writeBody := `{"name": "copy-src", "path": "data.txt", "content": "hello"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Copy
	copyBody := `{"name": "copy-src", "new_name": "copy-dst"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/copy", bytes.NewBufferString(copyBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("copy failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skill := data["skill"].(map[string]interface{})
	if skill["name"] != "copy-dst" {
		t.Errorf("expected name 'copy-dst', got %v", skill["name"])
	}

	// Verify source still exists
	if _, err := os.Stat(mgr.GlobalSkillPath("copy-src")); err != nil {
		t.Errorf("source should still exist: %v", err)
	}

	// Verify destination has the file
	dstDir := mgr.GlobalSkillPath("copy-dst")
	content, err := os.ReadFile(filepath.Join(dstDir, "data.txt"))
	if err != nil {
		t.Fatalf("data.txt not copied: %v", err)
	}
	if string(content) != "hello" {
		t.Errorf("expected 'hello', got %q", string(content))
	}

	// Verify SKILLS.md frontmatter has new name
	mdContent, _ := os.ReadFile(filepath.Join(dstDir, "SKILLS.md"))
	if !strings.Contains(string(mdContent), "name: copy-dst") {
		t.Errorf("SKILLS.md frontmatter not updated: %s", string(mdContent))
	}
}

func TestSkillCopyTargetExists(t *testing.T) {
	r, _ := setupSkillRouter()

	for _, name := range []string{"cp-src", "cp-dst"} {
		body := `{"name": "` + name + `", "description": "test"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	copyBody := `{"name": "cp-src", "new_name": "cp-dst"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/copy", bytes.NewBufferString(copyBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409 for existing target, got %d", w.Code)
	}
}

func TestSkillCopySameName(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"name": "cp-same", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	copyBody := `{"name": "cp-same", "new_name": "cp-same"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/copy", bytes.NewBufferString(copyBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for same name copy, got %d", w.Code)
	}
}

func TestAgentSkillCleanup(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create two skills
	for _, name := range []string{"keep-skill", "remove-skill"} {
		body := `{"name": "` + name + `", "description": "test"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

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
	r, mgr := setupSkillRouter()

	// Create two skills
	for _, name := range []string{"list-keep", "list-remove"} {
		body := `{"name": "` + name + `", "description": "test"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

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
	r, mgr := setupSkillRouter()

	// Create and load skills
	for _, name := range []string{"cache-a", "cache-b"} {
		body := `{"name": "` + name + `", "description": "test"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

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
	r, mgr := setupSkillRouter()

	// Create and load skills
	body := `{"name": "cache-all", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	loadBody := `{"skill_ids": ["cache-all"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/agents/del-all-agent/load", bytes.NewBufferString(loadBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
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

// =============================================
// Tests for code review fixes
// =============================================

// Fix #1: YAML injection via newlines in description
func TestSkillCreateDescriptionNewlinesSafe(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Description with newlines that could break YAML frontmatter
	body := `{"name": "yaml-nl", "description": "line1\nfoo: bar\n---\nevil: true"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}

	// Read SKILLS.md and verify the description is JSON-quoted (handles newlines)
	skillDir := mgr.GlobalSkillPath("yaml-nl")
	content, err := os.ReadFile(filepath.Join(skillDir, "SKILLS.md"))
	if err != nil {
		t.Fatalf("failed to read SKILLS.md: %v", err)
	}

	// The frontmatter should have exactly one --- ... --- block
	parts := strings.SplitN(string(content), "---", 3)
	if len(parts) < 3 {
		t.Fatalf("expected frontmatter delimiters, got:\n%s", string(content))
	}
	fm := parts[1]
	// The frontmatter should NOT contain "evil: true" as a separate YAML key
	if strings.Contains(fm, "evil:") {
		t.Errorf("YAML injection succeeded — frontmatter contains injected key:\n%s", fm)
	}
	// The description should be JSON-quoted with escaped newlines
	if !strings.Contains(fm, `\n`) {
		t.Errorf("expected JSON-escaped newlines in description, got:\n%s", fm)
	}
}

// Fix #1: Update also uses safe quoting
func TestSkillUpdateDescriptionNewlinesSafe(t *testing.T) {
	r, mgr := setupSkillRouter()

	body := `{"name": "upd-yaml", "description": "original"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Update with newline-containing description
	updBody := `{"name": "upd-yaml", "description": "line1\n---\nevil: true"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/update", bytes.NewBufferString(updBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}

	skillDir := mgr.GlobalSkillPath("upd-yaml")
	content, err := os.ReadFile(filepath.Join(skillDir, "SKILLS.md"))
	if err != nil {
		t.Fatalf("failed to read SKILLS.md: %v", err)
	}

	parts := strings.SplitN(string(content), "---", 3)
	if len(parts) < 3 {
		t.Fatalf("expected frontmatter delimiters, got:\n%s", string(content))
	}
	if strings.Contains(parts[1], "evil:") {
		t.Errorf("YAML injection via update succeeded:\n%s", parts[1])
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

// Fix #3: Export skips symlinks
func TestSkillExportSkipsSymlinks(t *testing.T) {
	r, mgr := setupSkillRouter()

	body := `{"name": "symlink-export", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Create a symlink inside the skill directory pointing outside
	skillDir := mgr.GlobalSkillPath("symlink-export")
	os.Symlink("/etc/hosts", filepath.Join(skillDir, "evil-link"))

	// Export
	req = httptest.NewRequest(http.MethodGet, "/v1/skills/export?name=symlink-export", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("export failed: %d", w.Code)
	}

	// Verify ZIP does NOT contain the symlink
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("failed to read ZIP: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "evil-link" {
			t.Error("ZIP should NOT contain symlink 'evil-link'")
		}
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
	r, mgr := setupSkillRouter()

	body := `{"name": "find-md", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

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
// Tests for code review error-handling fixes
// =============================================

// P0: Import handler should report HTTP status, not stale nil err, when server returns non-200.
func TestSkillImportNon200Response(t *testing.T) {
	r, _ := setupSkillRouter()

	// Stand up a server that returns 404
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	body := `{"name": "import-404", "zip_url": "` + server.URL + `/skill.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/import", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}

	// The error message must mention the HTTP status, not "<nil>"
	respBody := w.Body.String()
	if strings.Contains(respBody, "<nil>") {
		t.Errorf("response should not contain '<nil>' (stale err bug), got: %s", respBody)
	}
	if !strings.Contains(respBody, "404") {
		t.Errorf("response should mention HTTP 404 status, got: %s", respBody)
	}
}

// P2: FileWrite should return 500 when parent MkdirAll fails (short-circuit).
func TestSkillFileWriteReadOnlyParent(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create skill
	body := `{"name": "ro-parent", "description": "test"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/create", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}

	// Make the skill directory read-only so MkdirAll for a nested path fails
	skillDir := mgr.GlobalSkillPath("ro-parent")
	os.Chmod(skillDir, 0555)
	defer os.Chmod(skillDir, 0755) // restore for cleanup

	// Try to write a file in a new subdirectory — MkdirAll should fail
	writeBody := `{"name": "ro-parent", "path": "newdir/file.txt", "content": "hello"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/skills/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 when MkdirAll fails, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "parent directory") {
		t.Errorf("expected error about parent directory, got: %s", w.Body.String())
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
