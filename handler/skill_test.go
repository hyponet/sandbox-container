package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func setupSkillRouter() (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), "sandbox-skill-test-"+time.Now().Format("20060102150405"))
	os.MkdirAll(dir, 0755)
	mgr := session.NewManager(dir, 24*time.Hour)

	r := gin.New()
	skillH := NewSkillHandler(mgr)
	skills := r.Group("/v1/skills")
	{
		skills.POST("/list", skillH.List)
		skills.POST("/load", skillH.Load)
	}

	return r, mgr
}

// createTestZip creates a test ZIP file with a SKILLS.MD
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

func TestSkillListWithLocalFile(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create a test ZIP
	zipPath := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: test-skill\ndescription: A test skill\ntype: prompt\n---\nThis is the skill content.",
		"script.sh": "echo hello",
	})
	defer os.Remove(zipPath)

	// Create a simple HTTP server to serve the ZIP
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipPath)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	// Call list API
	body := `{"agent_id": "a1", "skill_urls": ["` + server.URL + `/skill.zip?slug=my-skill"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if success, ok := resp["success"].(bool); !ok || !success {
		t.Fatalf("expected success=true, got %v", resp)
	}

	data := resp["data"].(map[string]interface{})
	skills := data["skills"].([]interface{})
	if len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(skills))
	}

	skill := skills[0].(map[string]interface{})
	if skill["name"] != "test-skill" {
		t.Errorf("expected name 'test-skill', got %v", skill["name"])
	}
	if skill["description"] != "A test skill" {
		t.Errorf("expected description 'A test skill', got %v", skill["description"])
	}
	if skill["path"] != "/skills/my-skill" {
		t.Errorf("expected path '/skills/my-skill', got %v", skill["path"])
	}

	// Verify files were extracted
	skillDir := filepath.Join(mgr.SkillsRoot("a1"), "my-skill")
	if _, err := os.Stat(filepath.Join(skillDir, "SKILLS.MD")); err != nil {
		t.Errorf("SKILLS.MD not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "script.sh")); err != nil {
		t.Errorf("script.sh not extracted: %v", err)
	}
}

func TestSkillLoad(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Pre-populate skills directory
	skillDir := filepath.Join(mgr.SkillsRoot("a1"), "test-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILLS.MD"), []byte("---\nname: test-skill\n---\nSkill content here."), 0644)

	// Call load API
	body := `{"agent_id": "a1", "skill_names": ["test-skill"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/load", bytes.NewBufferString(body))
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
	if skill["name"] != "test-skill" {
		t.Errorf("expected name 'test-skill', got %v", skill["name"])
	}
	content := skill["content"].(string)
	if content != "---\nname: test-skill\n---\nSkill content here." {
		t.Errorf("unexpected content: %q", content)
	}
}

func TestSkillLoadNotFound(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"agent_id": "a1", "skill_names": ["nonexistent"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/load", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestSkillListNoAgentID(t *testing.T) {
	r, _ := setupSkillRouter()

	body := `{"skill_urls": ["http://example.com/skill.zip"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestExtractSlugFromURL(t *testing.T) {
	tests := []struct {
		url     string
		index   int
		want    string
	}{
		{"https://example.com/download?slug=my-skill", 0, "my-skill"},
		{"https://example.com/skills/my-skill.zip", 1, "my-skill"},
		{"https://example.com/path/to/skill-name.tar.gz", 2, "skill-name.tar"},
		{"https://example.com/", 3, "skill-3"},
		{":::invalid", 4, "skill-4"},
	}

	for _, tt := range tests {
		got := extractSlugFromURL(tt.url, tt.index)
		if got != tt.want {
			t.Errorf("extractSlugFromURL(%q, %d) = %q, want %q", tt.url, tt.index, got, tt.want)
		}
	}
}
