package handler

import (
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
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

func setupRegistryRouter() (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("sandbox-registry-test-%d-%d", time.Now().UnixNano(), os.Getpid()))
	os.MkdirAll(dir, 0755)
	globalSkillsDir := filepath.Join(dir, "global-skills")
	os.MkdirAll(globalSkillsDir, 0755)
	registryDir := filepath.Join(dir, "registry")
	os.MkdirAll(registryDir, 0755)

	mgr := session.NewManager(dir, 24*time.Hour)
	mgr.SetGlobalSkillsRoot(globalSkillsDir)
	mgr.SetRegistryRoot(registryDir)

	r := gin.New()
	registryH := NewRegistryHandler(mgr)
	registryH.SetSSRFProtection(false)

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
		registry.POST("/versions/clone", registryH.VersionClone)
		registry.POST("/versions/tree", registryH.VersionTree)
		registry.POST("/versions/file/read", registryH.VersionFileRead)
		registry.POST("/versions/file/write", registryH.VersionFileWrite)
		registry.POST("/versions/file/update", registryH.VersionFileUpdate)
		registry.POST("/versions/file/mkdir", registryH.VersionFileMkdir)
		registry.POST("/versions/file/delete", registryH.VersionFileDelete)
		registry.POST("/activate", registryH.Activate)
		registry.POST("/commit", registryH.Commit)
	}

	// Also set up agent skill routes for testing agent sync
	skillH := NewSkillHandler(mgr)
	agents := r.Group("/v1/skills/agents")
	{
		agents.POST("/:agent_id/list", skillH.AgentList)
		agents.POST("/:agent_id/load", skillH.AgentLoad)
	}

	return r, mgr
}

func doRequest(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func parseResponse(t *testing.T, w *httptest.ResponseRecorder) model.APIResponse {
	t.Helper()
	var resp model.APIResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v, body: %s", err, w.Body.String())
	}
	return resp
}

// ——— Skill CRUD ———

func TestRegistryCreate(t *testing.T) {
	r, _ := setupRegistryRouter()

	w := doRequest(t, r, "POST", "/v1/registry/create", `{"name": "test-skill", "description": "A test skill"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistrySkillCreateResult
	json.Unmarshal(data, &result)

	if result.Skill.Name != "test-skill" {
		t.Errorf("expected name test-skill, got %s", result.Skill.Name)
	}
	if result.Skill.ActiveVersion != "" {
		t.Errorf("expected empty active_version, got %s", result.Skill.ActiveVersion)
	}
	if len(result.Skill.Versions) != 0 {
		t.Errorf("expected empty versions, got %d", len(result.Skill.Versions))
	}
}

func TestRegistryCreateDuplicate(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "dup-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/create", `{"name": "dup-skill"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func TestRegistryCreateInvalidName(t *testing.T) {
	r, _ := setupRegistryRouter()

	w := doRequest(t, r, "POST", "/v1/registry/create", `{"name": "invalid name!"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRegistryGet(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "get-skill"}`)

	w := doRequest(t, r, "POST", "/v1/registry/get", `{"name": "get-skill"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("get failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistrySkillGetResult
	json.Unmarshal(data, &result)

	if result.Skill.Name != "get-skill" {
		t.Errorf("expected name get-skill, got %s", result.Skill.Name)
	}
	// No active version, so frontmatter/body should be empty
	if result.Frontmatter != "" || result.Body != "" {
		t.Errorf("expected empty frontmatter/body for skill with no active version")
	}
}

func TestRegistryGetWithActiveVersion(t *testing.T) {
	r, _ := setupRegistryRouter()

	// Create skill, create version with content, activate
	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "active-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "active-skill", "description": "v1"}`)
	resp := parseResponse(t, w)
	var vcResult model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vcResult)

	doRequest(t, r, "POST", "/v1/registry/activate", fmt.Sprintf(`{"name": "active-skill", "version": "%s"}`, vcResult.Version.Version))

	// Get should return active version's content
	w = doRequest(t, r, "POST", "/v1/registry/get", `{"name": "active-skill"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("get failed: %d %s", w.Code, w.Body.String())
	}

	resp = parseResponse(t, w)
	data, _ = json.Marshal(resp.Data)
	var getResult model.RegistrySkillGetResult
	json.Unmarshal(data, &getResult)

	if getResult.Skill.ActiveVersion != vcResult.Version.Version {
		t.Errorf("expected active_version %s, got %s", vcResult.Version.Version, getResult.Skill.ActiveVersion)
	}
}

func TestRegistryUpdate(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "update-skill", "description": "old"}`)
	w := doRequest(t, r, "POST", "/v1/registry/update", `{"name": "update-skill", "description": "new desc"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistrySkillUpdateResult
	json.Unmarshal(data, &result)

	if result.Skill.Description != "new desc" {
		t.Errorf("expected description 'new desc', got %s", result.Skill.Description)
	}
}

func TestRegistryDelete(t *testing.T) {
	r, mgr := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "del-skill"}`)
	// Create and activate a version so there's something deployed
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "del-skill"}`)
	resp := parseResponse(t, w)
	var vcResult model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vcResult)
	doRequest(t, r, "POST", "/v1/registry/activate", fmt.Sprintf(`{"name": "del-skill", "version": "%s"}`, vcResult.Version.Version))

	w = doRequest(t, r, "POST", "/v1/registry/delete", `{"name": "del-skill"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
	}

	// Verify registry dir is gone
	if _, err := os.Stat(mgr.RegistrySkillPath("del-skill")); err == nil {
		t.Error("registry directory should be deleted")
	}
	// Verify deployed dir is gone
	if _, err := os.Stat(mgr.GlobalSkillPath("del-skill")); err == nil {
		t.Error("deployed directory should be deleted")
	}
}

func TestRegistryList(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "skill-a"}`)
	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "skill-b"}`)

	w := doRequest(t, r, "POST", "/v1/registry/list", `{}`)
	if w.Code != http.StatusOK {
		t.Fatalf("list failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistrySkillListResult
	json.Unmarshal(data, &result)

	if len(result.Skills) != 2 {
		t.Errorf("expected 2 skills, got %d", len(result.Skills))
	}
}

func TestRegistryRename(t *testing.T) {
	r, mgr := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "old-name"}`)
	w := doRequest(t, r, "POST", "/v1/registry/rename", `{"name": "old-name", "new_name": "new-name"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("rename failed: %d %s", w.Code, w.Body.String())
	}

	// Verify old dir gone, new dir exists
	if _, err := os.Stat(mgr.RegistrySkillPath("old-name")); err == nil {
		t.Error("old registry directory should be gone")
	}
	if _, err := os.Stat(mgr.RegistrySkillPath("new-name")); err != nil {
		t.Error("new registry directory should exist")
	}
}

func TestRegistryCopy(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "src-skill"}`)
	// Create a version in source
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "src-skill", "description": "v1"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("version create failed: %d %s", w.Code, w.Body.String())
	}

	w = doRequest(t, r, "POST", "/v1/registry/copy", `{"name": "src-skill", "new_name": "dst-skill"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("copy failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistrySkillCopyResult
	json.Unmarshal(data, &result)

	if result.Skill.Name != "dst-skill" {
		t.Errorf("expected name dst-skill, got %s", result.Skill.Name)
	}
}

// ——— Version CRUD ———

func TestRegistryVersionCreate(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "v-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "v-skill", "description": "first version"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("version create failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistryVersionCreateResult
	json.Unmarshal(data, &result)

	if result.Version.Version == "" {
		t.Error("expected non-empty version")
	}
	if !strings.HasPrefix(result.Version.Version, "v") {
		t.Errorf("version should start with 'v', got %s", result.Version.Version)
	}
	if result.Version.Source != "manual" {
		t.Errorf("expected source 'manual', got %s", result.Version.Source)
	}
}

func TestRegistryVersionCreate_CopyFromActive(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "copy-skill"}`)
	// Create first version with a file
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "copy-skill"}`)
	resp := parseResponse(t, w)
	var vc1 model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc1)

	// Write a file to first version
	doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "copy-skill", "version": "%s", "path": "test.txt", "content": "hello"}`, vc1.Version.Version))

	// Activate first version
	doRequest(t, r, "POST", "/v1/registry/activate",
		fmt.Sprintf(`{"name": "copy-skill", "version": "%s"}`, vc1.Version.Version))

	// Create second version copying from active
	w = doRequest(t, r, "POST", "/v1/registry/versions/create",
		`{"name": "copy-skill", "copy_from_active": true}`)
	resp = parseResponse(t, w)
	var vc2 model.RegistryVersionCreateResult
	data, _ = json.Marshal(resp.Data)
	json.Unmarshal(data, &vc2)

	// Verify file was copied
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/read",
		fmt.Sprintf(`{"name": "copy-skill", "version": "%s", "path": "test.txt"}`, vc2.Version.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("file read failed: %d %s", w.Code, w.Body.String())
	}
	fileResp := parseResponse(t, w)
	fileData, _ := json.Marshal(fileResp.Data)
	var fileResult model.SkillFileReadResult
	json.Unmarshal(fileData, &fileResult)
	if fileResult.Content != "hello" {
		t.Errorf("expected 'hello', got %s", fileResult.Content)
	}
}

func TestRegistryVersionList(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "list-skill"}`)
	doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "list-skill", "description": "v1"}`)
	doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "list-skill", "description": "v2"}`)

	w := doRequest(t, r, "POST", "/v1/registry/versions/list", `{"name": "list-skill"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("version list failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistryVersionListResult
	json.Unmarshal(data, &result)

	if len(result.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(result.Versions))
	}
}

func TestRegistryVersionClone(t *testing.T) {
	r, _ := setupRegistryRouter()

	// Create skill
	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "clone-skill"}`)

	// Create a version
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "clone-skill", "description": "original"}`)
	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var vc model.RegistryVersionCreateResult
	json.Unmarshal(data, &vc)
	srcVersion := vc.Version.Version

	// Write a file to the source version
	doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "clone-skill", "version": "%s", "path": "test.txt", "content": "clone me"}`, srcVersion))

	// Clone the version
	w = doRequest(t, r, "POST", "/v1/registry/versions/clone",
		fmt.Sprintf(`{"name": "clone-skill", "version": "%s", "description": "cloned version"}`, srcVersion))
	if w.Code != http.StatusOK {
		t.Fatalf("version clone failed: %d %s", w.Code, w.Body.String())
	}

	resp = parseResponse(t, w)
	data, _ = json.Marshal(resp.Data)
	var cloneResult model.RegistryVersionCloneResult
	json.Unmarshal(data, &cloneResult)

	if cloneResult.Version.Version == "" {
		t.Error("expected non-empty version")
	}
	if cloneResult.Version.Version == srcVersion {
		t.Error("cloned version should differ from source")
	}
	if cloneResult.Version.Source != "clone:"+srcVersion {
		t.Errorf("expected source 'clone:%s', got %s", srcVersion, cloneResult.Version.Source)
	}
	if cloneResult.Version.Description != "cloned version" {
		t.Errorf("expected description 'cloned version', got %s", cloneResult.Version.Description)
	}

	// Verify the cloned file content
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/read",
		fmt.Sprintf(`{"name": "clone-skill", "version": "%s", "path": "test.txt"}`, cloneResult.Version.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("file read failed: %d %s", w.Code, w.Body.String())
	}
	fileResp := parseResponse(t, w)
	fileData, _ := json.Marshal(fileResp.Data)
	var fileResult model.SkillFileReadResult
	json.Unmarshal(fileData, &fileResult)
	if fileResult.Content != "clone me" {
		t.Errorf("expected 'clone me', got %s", fileResult.Content)
	}

	// Verify skill now has 2 versions
	if len(cloneResult.Skill.Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(cloneResult.Skill.Versions))
	}
}

func TestRegistryVersionCloneWithTargetVersion(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "clone-target-skill"}`)

	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "clone-target-skill", "description": "original"}`)
	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var vc model.RegistryVersionCreateResult
	json.Unmarshal(data, &vc)
	srcVersion := vc.Version.Version

	doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "clone-target-skill", "version": "%s", "path": "test.txt", "content": "copy me"}`, srcVersion))

	targetVersion := fmt.Sprintf("v%d-deadbeef", time.Now().UnixNano())
	w = doRequest(t, r, "POST", "/v1/registry/versions/clone",
		fmt.Sprintf(`{"name": "clone-target-skill", "version": "%s", "new_version": "%s", "description": "cloned version"}`, srcVersion, targetVersion))
	if w.Code != http.StatusOK {
		t.Fatalf("version clone failed: %d %s", w.Code, w.Body.String())
	}

	resp = parseResponse(t, w)
	data, _ = json.Marshal(resp.Data)
	var cloneResult model.RegistryVersionCloneResult
	json.Unmarshal(data, &cloneResult)

	if cloneResult.Version.Version != targetVersion {
		t.Fatalf("expected version %s, got %s", targetVersion, cloneResult.Version.Version)
	}
	if cloneResult.Version.Source != "clone:"+srcVersion {
		t.Fatalf("expected source clone:%s, got %s", srcVersion, cloneResult.Version.Source)
	}
}

func TestRegistryVersionCloneNotFound(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "clone-nf-skill"}`)

	w := doRequest(t, r, "POST", "/v1/registry/versions/clone",
		`{"name": "clone-nf-skill", "version": "v1-aabbccdd"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestRegistryVersionCloneMissingSourceDirectory(t *testing.T) {
	r, mgr := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "clone-missing-dir-skill"}`)

	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "clone-missing-dir-skill", "description": "original"}`)
	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var vc model.RegistryVersionCreateResult
	json.Unmarshal(data, &vc)
	srcVersion := vc.Version.Version

	srcVersionDir := filepath.Join(mgr.RegistrySkillPath("clone-missing-dir-skill"), srcVersion)
	if err := os.RemoveAll(srcVersionDir); err != nil {
		t.Fatalf("failed to remove source version dir: %v", err)
	}

	w = doRequest(t, r, "POST", "/v1/registry/versions/clone",
		fmt.Sprintf(`{"name": "clone-missing-dir-skill", "version": "%s"}`, srcVersion))
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "version directory not found") {
		t.Fatalf("expected missing source directory error, got %s", w.Body.String())
	}
}

func TestRegistryVersionCloneTargetVersionExists(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "clone-conflict-skill"}`)

	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "clone-conflict-skill", "description": "source"}`)
	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var sourceResult model.RegistryVersionCreateResult
	json.Unmarshal(data, &sourceResult)

	w = doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "clone-conflict-skill", "description": "target"}`)
	resp = parseResponse(t, w)
	data, _ = json.Marshal(resp.Data)
	var targetResult model.RegistryVersionCreateResult
	json.Unmarshal(data, &targetResult)

	w = doRequest(t, r, "POST", "/v1/registry/versions/clone",
		fmt.Sprintf(`{"name": "clone-conflict-skill", "version": "%s", "new_version": "%s"}`, sourceResult.Version.Version, targetResult.Version.Version))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", w.Code, w.Body.String())
	}
}

func TestRegistryVersionCloneSkillNotFound(t *testing.T) {
	r, _ := setupRegistryRouter()

	w := doRequest(t, r, "POST", "/v1/registry/versions/clone",
		`{"name": "no-such-skill", "version": "v1-aabbccdd"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestRegistryVersionDelete_ActiveVersion(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "delv-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "delv-skill"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	// Activate
	doRequest(t, r, "POST", "/v1/registry/activate",
		fmt.Sprintf(`{"name": "delv-skill", "version": "%s"}`, vc.Version.Version))

	// Try to delete active version - should fail
	w = doRequest(t, r, "POST", "/v1/registry/versions/delete",
		fmt.Sprintf(`{"name": "delv-skill", "version": "%s"}`, vc.Version.Version))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 when deleting active version, got %d", w.Code)
	}
}

// ——— Version File Operations ———

func TestRegistryVersionFileReadWrite(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "frw-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "frw-skill"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	// Write
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "frw-skill", "version": "%s", "path": "hello.txt", "content": "world"}`, vc.Version.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("file write failed: %d %s", w.Code, w.Body.String())
	}

	// Read
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/read",
		fmt.Sprintf(`{"name": "frw-skill", "version": "%s", "path": "hello.txt"}`, vc.Version.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("file read failed: %d %s", w.Code, w.Body.String())
	}
	fileResp := parseResponse(t, w)
	fileData, _ := json.Marshal(fileResp.Data)
	var fileResult model.SkillFileReadResult
	json.Unmarshal(fileData, &fileResult)
	if fileResult.Content != "world" {
		t.Errorf("expected 'world', got %s", fileResult.Content)
	}
}

func TestRegistryVersionFile_MetaProtection(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "meta-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "meta-skill"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	// Write _meta.json should fail
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "meta-skill", "version": "%s", "path": "_meta.json", "content": "{}"}`, vc.Version.Version))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for writing _meta.json, got %d", w.Code)
	}

	// Update _meta.json should fail
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/update",
		fmt.Sprintf(`{"name": "meta-skill", "version": "%s", "path": "_meta.json", "old_str": "x", "new_str": "y"}`, vc.Version.Version))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for updating _meta.json, got %d", w.Code)
	}

	// Delete _meta.json should fail
	w = doRequest(t, r, "POST", "/v1/registry/versions/file/delete",
		fmt.Sprintf(`{"name": "meta-skill", "version": "%s", "path": "_meta.json"}`, vc.Version.Version))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for deleting _meta.json, got %d", w.Code)
	}
}

func TestRegistryVersionFile_PathTraversal(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "pt-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "pt-skill"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	w = doRequest(t, r, "POST", "/v1/registry/versions/file/read",
		fmt.Sprintf(`{"name": "pt-skill", "version": "%s", "path": "../../../etc/passwd"}`, vc.Version.Version))
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for path traversal, got %d", w.Code)
	}
}

// ——— Activate ———

func TestRegistryActivate(t *testing.T) {
	r, mgr := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "act-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "act-skill", "description": "v1"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	// Write a file to the version
	doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "act-skill", "version": "%s", "path": "SKILLS.md", "content": "---\\nname: act-skill\\n---\\nHello"}`, vc.Version.Version))

	// Activate
	w = doRequest(t, r, "POST", "/v1/registry/activate",
		fmt.Sprintf(`{"name": "act-skill", "version": "%s"}`, vc.Version.Version))
	if w.Code != http.StatusOK {
		t.Fatalf("activate failed: %d %s", w.Code, w.Body.String())
	}

	// Verify files deployed to /data/skills/
	deployedDir := mgr.GlobalSkillPath("act-skill")
	if _, err := os.Stat(deployedDir); err != nil {
		t.Fatalf("deployed directory should exist: %v", err)
	}

	// Verify SKILLS.md exists in deployed dir
	if _, err := os.Stat(filepath.Join(deployedDir, "SKILLS.md")); err != nil {
		t.Fatalf("SKILLS.md should exist in deployed dir: %v", err)
	}

	// Verify _meta.json exists in deployed dir (old format for syncSkillToAgent compat)
	deployedMeta, err := readSkillMeta(deployedDir)
	if err != nil {
		t.Fatalf("failed to read deployed _meta.json: %v", err)
	}
	if deployedMeta.Name != "act-skill" {
		t.Errorf("expected deployed name 'act-skill', got %s", deployedMeta.Name)
	}
}

func TestRegistryActivate_Idempotent(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "idem-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "idem-skill"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	// Activate twice
	doRequest(t, r, "POST", "/v1/registry/activate",
		fmt.Sprintf(`{"name": "idem-skill", "version": "%s"}`, vc.Version.Version))
	w = doRequest(t, r, "POST", "/v1/registry/activate",
		fmt.Sprintf(`{"name": "idem-skill", "version": "%s"}`, vc.Version.Version))
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 on re-activate, got %d", w.Code)
	}
}

// ——— Commit ———

func TestRegistryCommit(t *testing.T) {
	r, mgr := setupRegistryRouter()

	// Set up agent skills directory with a skill
	agentSkillsDir := mgr.SkillsRoot("agent-1")
	skillDir := filepath.Join(agentSkillsDir, "commit-skill")
	os.MkdirAll(skillDir, 0755)

	// Write files in agent skill dir
	meta := &model.SkillMetaJSON{
		Name:        "commit-skill",
		Description: "agent version",
		CreatedAt:   time.Now().UnixNano(),
		UpdatedAt:   time.Now().UnixNano(),
	}
	writeSkillMeta(skillDir, meta)
	os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte("---\nname: commit-skill\n---\nAgent content"), 0644)

	// Commit
	w := doRequest(t, r, "POST", "/v1/registry/commit",
		`{"name": "commit-skill", "agent_id": "agent-1", "description": "from agent", "activate": false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("commit failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistryCommitResult
	json.Unmarshal(data, &result)

	if result.Version.Version == "" {
		t.Error("expected non-empty version")
	}
	if result.Version.Source != "agent:agent-1" {
		t.Errorf("expected source 'agent:agent-1', got %s", result.Version.Source)
	}
	if result.Skill.Name != "commit-skill" {
		t.Errorf("expected skill name 'commit-skill', got %s", result.Skill.Name)
	}

	// Verify _meta.json was NOT copied to version dir (only skill files)
	versionDir := filepath.Join(mgr.RegistrySkillPath("commit-skill"), result.Version.Version)
	if _, err := os.Stat(filepath.Join(versionDir, "_meta.json")); err == nil {
		t.Error("_meta.json should not exist in version directory (commit skips it)")
	}

	// Verify SKILLS.md was copied
	if _, err := os.Stat(filepath.Join(versionDir, "SKILLS.md")); err != nil {
		t.Error("SKILLS.md should exist in version directory")
	}
}

func TestRegistryCommit_WithActivate(t *testing.T) {
	r, mgr := setupRegistryRouter()

	agentSkillsDir := mgr.SkillsRoot("agent-2")
	skillDir := filepath.Join(agentSkillsDir, "ca-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte("---\nname: ca-skill\n---\nContent"), 0644)

	w := doRequest(t, r, "POST", "/v1/registry/commit",
		`{"name": "ca-skill", "agent_id": "agent-2", "activate": true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("commit failed: %d %s", w.Code, w.Body.String())
	}

	// Verify deployed
	deployedDir := mgr.GlobalSkillPath("ca-skill")
	if _, err := os.Stat(deployedDir); err != nil {
		t.Fatalf("deployed directory should exist after commit+activate: %v", err)
	}
}

func TestRegistryCommit_SourceNotFound(t *testing.T) {
	r, _ := setupRegistryRouter()

	w := doRequest(t, r, "POST", "/v1/registry/commit",
		`{"name": "no-skill", "agent_id": "agent-x", "activate": false}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestRegistryCommit_AutoCreateSkill(t *testing.T) {
	r, mgr := setupRegistryRouter()

	// Create agent skill without registry entry
	agentSkillsDir := mgr.SkillsRoot("agent-3")
	skillDir := filepath.Join(agentSkillsDir, "new-skill")
	os.MkdirAll(skillDir, 0755)
	os.WriteFile(filepath.Join(skillDir, "SKILLS.md"), []byte("---\nname: new-skill\n---\nNew"), 0644)

	w := doRequest(t, r, "POST", "/v1/registry/commit",
		`{"name": "new-skill", "agent_id": "agent-3", "activate": false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("commit failed: %d %s", w.Code, w.Body.String())
	}

	// Verify registry entry was auto-created
	if _, err := os.Stat(mgr.RegistrySkillPath("new-skill")); err != nil {
		t.Fatalf("registry entry should be auto-created: %v", err)
	}
}

// ——— Import ———

func TestRegistryImport(t *testing.T) {
	r, mgr := setupRegistryRouter()

	// Create a test ZIP server
	zipContent := createTestZip(t, map[string]string{
		"SKILLS.md": "---\nname: imp-skill\ndescription: \"Imported\"\n---\nImported content",
		"script.py": "print('hello')",
	})
	defer os.Remove(zipContent)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipContent)
	}))
	defer ts.Close()

	w := doRequest(t, r, "POST", "/v1/registry/import",
		fmt.Sprintf(`{"name": "imp-skill", "zip_url": "%s"}`, ts.URL))
	if w.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", w.Code, w.Body.String())
	}

	resp := parseResponse(t, w)
	data, _ := json.Marshal(resp.Data)
	var result model.RegistryImportResult
	json.Unmarshal(data, &result)

	if result.Version.Version == "" {
		t.Error("expected non-empty version")
	}
	if result.Version.Source != "import" {
		t.Errorf("expected source 'import', got %s", result.Version.Source)
	}
	if result.Skill.ActiveVersion == "" {
		t.Error("expected auto-activate after import")
	}

	// Verify deployed
	deployedDir := mgr.GlobalSkillPath("imp-skill")
	if _, err := os.Stat(deployedDir); err != nil {
		t.Fatalf("deployed directory should exist after import: %v", err)
	}
}

func TestRegistryImport_AutoCreateSkill(t *testing.T) {
	r, mgr := setupRegistryRouter()

	zipContent := createTestZip(t, map[string]string{
		"SKILLS.md": "---\nname: new-imp\n---\nContent",
	})
	defer os.Remove(zipContent)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, zipContent)
	}))
	defer ts.Close()

	w := doRequest(t, r, "POST", "/v1/registry/import",
		fmt.Sprintf(`{"name": "new-imp", "zip_url": "%s"}`, ts.URL))
	if w.Code != http.StatusOK {
		t.Fatalf("import failed: %d %s", w.Code, w.Body.String())
	}

	// Verify registry entry was auto-created
	if _, err := os.Stat(mgr.RegistrySkillPath("new-imp")); err != nil {
		t.Fatalf("registry entry should be auto-created: %v", err)
	}
}

// ——— Export ———

func TestRegistryExport_ActiveVersion(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "exp-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "exp-skill"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	// Write a file
	doRequest(t, r, "POST", "/v1/registry/versions/file/write",
		fmt.Sprintf(`{"name": "exp-skill", "version": "%s", "path": "test.txt", "content": "export me"}`, vc.Version.Version))

	// Activate
	doRequest(t, r, "POST", "/v1/registry/activate",
		fmt.Sprintf(`{"name": "exp-skill", "version": "%s"}`, vc.Version.Version))

	// Export active version
	w = doRequest(t, r, "GET", "/v1/registry/export?name=exp-skill", "")
	if w.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}

	ct := w.Header().Get("Content-Type")
	if ct != "application/zip" {
		t.Errorf("expected application/zip, got %s", ct)
	}
}

func TestRegistryExport_SpecificVersion(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "expv-skill"}`)
	w := doRequest(t, r, "POST", "/v1/registry/versions/create", `{"name": "expv-skill"}`)
	resp := parseResponse(t, w)
	var vc model.RegistryVersionCreateResult
	data, _ := json.Marshal(resp.Data)
	json.Unmarshal(data, &vc)

	// Export specific version
	w = doRequest(t, r, "GET", fmt.Sprintf("/v1/registry/export?name=expv-skill&version=%s", vc.Version.Version), "")
	if w.Code != http.StatusOK {
		t.Fatalf("export failed: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistryExport_NonExistentVersion(t *testing.T) {
	r, _ := setupRegistryRouter()

	doRequest(t, r, "POST", "/v1/registry/create", `{"name": "expn-skill"}`)
	w := doRequest(t, r, "GET", "/v1/registry/export?name=expn-skill&version=v999999-deadbeef", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
