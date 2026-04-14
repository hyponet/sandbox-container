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
	skillH.SetSSRFProtection(false) // disable SSRF for tests using httptest (loopback)
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

func TestSkillListCaching(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create a test ZIP
	zipPath := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: cached-skill\ndescription: cached\n---\nContent.",
	})
	defer os.Remove(zipPath)

	downloadCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/skill.zip", func(w http.ResponseWriter, r *http.Request) {
		downloadCount++
		http.ServeFile(w, r, zipPath)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	skillURL := server.URL + "/skill.zip?slug=cached-skill"

	// First call: should download
	body := `{"agent_id": "a1", "skill_urls": ["` + skillURL + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first call failed: %d %s", w.Code, w.Body.String())
	}
	if downloadCount != 1 {
		t.Fatalf("expected 1 download, got %d", downloadCount)
	}

	// Second call with same URL: should use cache, no new download
	req2 := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("second call failed: %d %s", w2.Code, w2.Body.String())
	}
	if downloadCount != 1 {
		t.Fatalf("expected no new download (still 1), got %d", downloadCount)
	}

	// Verify .source file exists
	sourcePath := filepath.Join(mgr.SkillsRoot("a1"), "cached-skill", ".source")
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf(".source file not found: %v", err)
	}
	if string(data) != skillURL {
		t.Errorf("expected .source to be %q, got %q", skillURL, string(data))
	}
}

func TestSkillListCleanup(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Create two test ZIPs
	zipA := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: skill-a\n---\nA content.",
	})
	defer os.Remove(zipA)
	zipB := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: skill-b\n---\nB content.",
	})
	defer os.Remove(zipB)

	mux := http.NewServeMux()
	mux.HandleFunc("/a.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipA)
	})
	mux.HandleFunc("/b.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipB)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	urlA := server.URL + "/a.zip?slug=skill-a"
	urlB := server.URL + "/b.zip?slug=skill-b"

	// First call: download both
	body := `{"agent_id": "a1", "skill_urls": ["` + urlA + `", "` + urlB + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("first call failed: %d %s", w.Code, w.Body.String())
	}

	skillsRoot := mgr.SkillsRoot("a1")
	if _, err := os.Stat(filepath.Join(skillsRoot, "skill-a")); err != nil {
		t.Fatalf("skill-a not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillsRoot, "skill-b")); err != nil {
		t.Fatalf("skill-b not created: %v", err)
	}

	// Second call: only skill-a, skill-b should be cleaned up
	body2 := `{"agent_id": "a1", "skill_urls": ["` + urlA + `"]}`
	req2 := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("second call failed: %d %s", w2.Code, w2.Body.String())
	}

	// skill-a should still exist (cached)
	if _, err := os.Stat(filepath.Join(skillsRoot, "skill-a")); err != nil {
		t.Fatalf("skill-a should still exist: %v", err)
	}
	// skill-b should be cleaned up
	if _, err := os.Stat(filepath.Join(skillsRoot, "skill-b")); err == nil {
		t.Fatalf("skill-b should have been cleaned up")
	}
}

func TestSkillListEmptyURLsCleansAll(t *testing.T) {
	r, mgr := setupSkillRouter()

	// Pre-populate skill directories
	skillsRoot := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsRoot, "orphan-skill"), 0755)
	os.WriteFile(filepath.Join(skillsRoot, "orphan-skill", "SKILLS.MD"), []byte("content"), 0644)

	// Call with empty skill_urls
	body := `{"agent_id": "a1", "skill_urls": []}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("call failed: %d %s", w.Code, w.Body.String())
	}

	// orphan-skill should be cleaned up
	if _, err := os.Stat(filepath.Join(skillsRoot, "orphan-skill")); err == nil {
		t.Fatalf("orphan-skill should have been cleaned up")
	}
}

func TestSkillListCacheInvalidationOnURLChange(t *testing.T) {
	r, mgr := setupSkillRouter()

	zipV1 := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: skill-v1\n---\nv1.",
	})
	defer os.Remove(zipV1)
	zipV2 := createTestZip(t, map[string]string{
		"SKILLS.MD": "---\nname: skill-v2\n---\nv2.",
	})
	defer os.Remove(zipV2)

	mux := http.NewServeMux()
	mux.HandleFunc("/v1.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipV1)
	})
	mux.HandleFunc("/v2.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipV2)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	slug := "my-slug"
	urlV1 := server.URL + "/v1.zip?slug=" + slug
	urlV2 := server.URL + "/v2.zip?slug=" + slug

	// First call with v1 URL
	body1 := `{"agent_id": "a1", "skill_urls": ["` + urlV1 + `"]}`
	req1 := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)

	if w1.Code != http.StatusOK {
		t.Fatalf("first call failed: %d", w1.Code)
	}

	// Second call with v2 URL (same slug, different URL): should re-download
	body2 := `{"agent_id": "a1", "skill_urls": ["` + urlV2 + `"]}`
	req2 := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("second call failed: %d", w2.Code)
	}

	// Verify metadata changed to v2
	var resp map[string]interface{}
	json.Unmarshal(w2.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	skills := data["skills"].([]interface{})
	skill := skills[0].(map[string]interface{})
	if skill["name"] != "skill-v2" {
		t.Errorf("expected name 'skill-v2', got %v", skill["name"])
	}

	// Verify .source updated
	sourceData, _ := os.ReadFile(filepath.Join(mgr.SkillsRoot("a1"), slug, ".source"))
	if string(sourceData) != urlV2 {
		t.Errorf("expected .source to be %q, got %q", urlV2, string(sourceData))
	}
}

func TestExtractSlugFromURL(t *testing.T) {
	tests := []struct {
		url   string
		index int
		want  string
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

func TestSkillListSSRFBlocksPrivateURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), "sandbox-skill-test-ssrf-"+time.Now().Format("20060102150405"))
	os.MkdirAll(dir, 0755)
	mgr := session.NewManager(dir, 24*time.Hour)

	r := gin.New()
	skillH := NewSkillHandler(mgr)
	// SSRF protection enabled (default)
	skills := r.Group("/v1/skills")
	skills.POST("/list", skillH.List)

	body := `{"agent_id": "a1", "skill_urls": ["http://127.0.0.1/skill.zip"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skills/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 (SSRF blocked), got %d %s", w.Code, w.Body.String())
	}
}
