package handler

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

var validVersionID = regexp.MustCompile(`^v\d+-[0-9a-f]{8}$`)

func validateVersionID(version string) error {
	if version == "" {
		return fmt.Errorf("version is required")
	}
	if len(version) > 40 {
		return fmt.Errorf("version ID too long (max 40 chars)")
	}
	if !validVersionID.MatchString(version) {
		return fmt.Errorf("version ID must match v<number>-<hex> format")
	}
	return nil
}

// RegistryHandler handles skill registry API endpoints.
type RegistryHandler struct {
	mgr            *session.Manager
	mu             sync.RWMutex
	httpClient     *http.Client
	ssrfProtection bool
}

// NewRegistryHandler creates a new RegistryHandler.
func NewRegistryHandler(mgr *session.Manager) *RegistryHandler {
	return &RegistryHandler{
		mgr:            mgr,
		ssrfProtection: isSSRFProtectionEnabled(),
	}
}

// SetSSRFProtection enables or disables SSRF protection.
func (h *RegistryHandler) SetSSRFProtection(enabled bool) {
	h.ssrfProtection = enabled
}

func (h *RegistryHandler) client() *http.Client {
	if h.httpClient != nil {
		return h.httpClient
	}
	return defaultHTTPClient
}

// ——— Registry Meta Helpers ———

func readRegistryMeta(skillDir string) (*model.RegistryMetaJSON, error) {
	data, err := os.ReadFile(filepath.Join(skillDir, metaFile))
	if err != nil {
		return nil, err
	}
	var meta model.RegistryMetaJSON
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

func writeRegistryMeta(skillDir string, meta *model.RegistryMetaJSON) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(skillDir, metaFile), data, 0644)
}

func allocateVersion() string {
	var buf [4]byte
	rand.Read(buf[:])
	return fmt.Sprintf("v%d-%s", time.Now().UnixNano(), hex.EncodeToString(buf[:]))
}

// deployVersionToSkills copies a registry version directory to /data/skills/<name>/
// and writes a SkillMetaJSON for backward compatibility with syncSkillToAgent.
func (h *RegistryHandler) deployVersionToSkills(registrySkillDir string, meta *model.RegistryMetaJSON, versionEntry *model.RegistryVersionEntry) error {
	versionDir := filepath.Join(registrySkillDir, versionEntry.Version)
	skillDir := h.mgr.GlobalSkillPath(meta.Name)

	if err := os.RemoveAll(skillDir); err != nil {
		return fmt.Errorf("failed to remove old deployment: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(skillDir), 0755); err != nil {
		return err
	}
	if err := copyDir(versionDir, skillDir); err != nil {
		return err
	}

	// Write old-format SkillMetaJSON for syncSkillToAgent compatibility
	now := time.Now().UnixNano()
	skillMeta := &model.SkillMetaJSON{
		Name:        meta.Name,
		Description: meta.Description,
		CreatedAt:   meta.CreatedAt,
		UpdatedAt:   now,
	}
	return writeSkillMeta(skillDir, skillMeta)
}

// findVersionEntry finds a version entry in the registry meta.
func findVersionEntry(meta *model.RegistryMetaJSON, version string) (*model.RegistryVersionEntry, bool) {
	for i := range meta.Versions {
		if meta.Versions[i].Version == version {
			return &meta.Versions[i], true
		}
	}
	return nil, false
}

// ——— Skill-level CRUD ———

// Create creates a new empty skill in the registry.
func (h *RegistryHandler) Create(c *gin.Context) {
	var req model.RegistrySkillCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	if _, err := os.Stat(skillDir); err == nil {
		c.JSON(http.StatusConflict, model.ErrResponse("skill already exists: "+req.Name))
		return
	}

	if err := os.MkdirAll(skillDir, 0755); err != nil {
		log.Printf("[ERROR] Registry Create: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create skill directory: "+err.Error()))
		return
	}

	now := time.Now().UnixNano()
	meta := &model.RegistryMetaJSON{
		Name:          req.Name,
		Description:   req.Description,
		CreatedAt:     now,
		UpdatedAt:     now,
		ActiveVersion: "",
		Versions:      []model.RegistryVersionEntry{},
	}

	if err := writeRegistryMeta(skillDir, meta); err != nil {
		os.RemoveAll(skillDir)
		log.Printf("[ERROR] Registry Create: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistrySkillCreateResult{Skill: *meta}))
}

// Get retrieves a skill's metadata and active version's SKILLS.md content.
func (h *RegistryHandler) Get(c *gin.Context) {
	var req model.RegistrySkillGetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
			return
		}
		log.Printf("[ERROR] Registry Get: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	var frontmatter, body string
	if meta.ActiveVersion != "" {
		versionDir := filepath.Join(skillDir, meta.ActiveVersion)
		if content, err := readSkillsMD(versionDir, req.Name); err == nil {
			frontmatter, body = splitFrontmatter(content)
		}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistrySkillGetResult{
		Skill:       *meta,
		Frontmatter: frontmatter,
		Body:        body,
	}))
}

// Update updates a skill's description.
func (h *RegistryHandler) Update(c *gin.Context) {
	var req model.RegistrySkillUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
			return
		}
		log.Printf("[ERROR] Registry Update: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	meta.Description = req.Description
	meta.UpdatedAt = time.Now().UnixNano()

	if err := writeRegistryMeta(skillDir, meta); err != nil {
		log.Printf("[ERROR] Registry Update: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistrySkillUpdateResult{Skill: *meta}))
}

// Delete removes a skill from the registry and its deployment in /data/skills/.
func (h *RegistryHandler) Delete(c *gin.Context) {
	var req model.RegistrySkillDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	if _, err := os.Stat(skillDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	// Remove registry directory
	if err := os.RemoveAll(skillDir); err != nil {
		log.Printf("[ERROR] Registry Delete: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to delete skill: "+err.Error()))
		return
	}

	// Remove deployed skill if exists
	deployedDir := h.mgr.GlobalSkillPath(req.Name)
	os.RemoveAll(deployedDir)

	c.JSON(http.StatusOK, model.OkMsg("skill deleted: "+req.Name))
}

// List lists all skills in the registry.
func (h *RegistryHandler) List(c *gin.Context) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	registryDir := h.mgr.RegistryRoot()
	entries, err := os.ReadDir(registryDir)
	if err != nil {
		c.JSON(http.StatusOK, model.OkResponse(model.RegistrySkillListResult{Skills: []model.RegistryMetaJSON{}}))
		return
	}

	var skills []model.RegistryMetaJSON
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		meta, err := readRegistryMeta(filepath.Join(registryDir, entry.Name()))
		if err != nil {
			continue
		}
		skills = append(skills, *meta)
	}

	if skills == nil {
		skills = []model.RegistryMetaJSON{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistrySkillListResult{Skills: skills}))
}

// Rename renames a skill in the registry and its deployment.
func (h *RegistryHandler) Rename(c *gin.Context) {
	var req model.RegistrySkillRenameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := validateSkillID(req.NewName); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("new_name: "+err.Error()))
		return
	}
	if req.Name == req.NewName {
		c.JSON(http.StatusBadRequest, model.ErrResponse("new_name must differ from current name"))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	oldDir := h.mgr.RegistrySkillPath(req.Name)
	if _, err := os.Stat(oldDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	newDir := h.mgr.RegistrySkillPath(req.NewName)
	if _, err := os.Stat(newDir); err == nil {
		c.JSON(http.StatusConflict, model.ErrResponse("target skill already exists: "+req.NewName))
		return
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		log.Printf("[ERROR] Registry Rename: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to rename skill: "+err.Error()))
		return
	}

	meta, err := readRegistryMeta(newDir)
	if err != nil {
		os.Rename(newDir, oldDir) // best-effort rollback
		log.Printf("[ERROR] Registry Rename: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	meta.Name = req.NewName
	meta.UpdatedAt = time.Now().UnixNano()
	if err := writeRegistryMeta(newDir, meta); err != nil {
		os.Rename(newDir, oldDir) // best-effort rollback
		log.Printf("[ERROR] Registry Rename: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	// Rename deployed directory if exists
	oldDeployed := h.mgr.GlobalSkillPath(req.Name)
	newDeployed := h.mgr.GlobalSkillPath(req.NewName)
	if _, err := os.Stat(oldDeployed); err == nil {
		if err := os.Rename(oldDeployed, newDeployed); err != nil {
			log.Printf("[ERROR] Registry Rename: rename deployed dir: %v", err)
			// Registry meta already updated; log but don't fail the request
		} else {
			// Update deployed _meta.json
			if deployedMeta, err := readSkillMeta(newDeployed); err == nil {
				deployedMeta.Name = req.NewName
				deployedMeta.UpdatedAt = time.Now().UnixNano()
				writeSkillMeta(newDeployed, deployedMeta)
			}
		}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistrySkillRenameResult{Skill: *meta}))
}

// Copy copies a skill with all its versions to a new name.
func (h *RegistryHandler) Copy(c *gin.Context) {
	var req model.RegistrySkillCopyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := validateSkillID(req.NewName); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("new_name: "+err.Error()))
		return
	}
	if req.Name == req.NewName {
		c.JSON(http.StatusBadRequest, model.ErrResponse("new_name must differ from source name"))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	srcDir := h.mgr.RegistrySkillPath(req.Name)
	if _, err := os.Stat(srcDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
		return
	}

	dstDir := h.mgr.RegistrySkillPath(req.NewName)
	if _, err := os.Stat(dstDir); err == nil {
		c.JSON(http.StatusConflict, model.ErrResponse("target skill already exists: "+req.NewName))
		return
	}

	if err := os.MkdirAll(dstDir, 0755); err != nil {
		log.Printf("[ERROR] Registry Copy: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create target directory: "+err.Error()))
		return
	}

	if err := copyDir(srcDir, dstDir); err != nil {
		os.RemoveAll(dstDir)
		log.Printf("[ERROR] Registry Copy: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to copy skill: "+err.Error()))
		return
	}

	// Generate new _meta.json based on source
	now := time.Now().UnixNano()
	srcMeta, _ := readRegistryMeta(srcDir)
	desc := ""
	if srcMeta != nil {
		desc = srcMeta.Description
	}
	// Copy version entries from source (file content already copied via copyDir)
	var versions []model.RegistryVersionEntry
	if srcMeta != nil {
		versions = make([]model.RegistryVersionEntry, len(srcMeta.Versions))
		copy(versions, srcMeta.Versions)
	}
	meta := &model.RegistryMetaJSON{
		Name:          req.NewName,
		Description:   desc,
		CreatedAt:     now,
		UpdatedAt:     now,
		ActiveVersion: "",
		Versions:      versions,
	}
	if err := writeRegistryMeta(dstDir, meta); err != nil {
		os.RemoveAll(dstDir)
		log.Printf("[ERROR] Registry Copy: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistrySkillCopyResult{Skill: *meta}))
}

// ——— Version-level CRUD ———

// VersionCreate creates a new version for a skill.
func (h *RegistryHandler) VersionCreate(c *gin.Context) {
	var req model.RegistryVersionCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
			return
		}
		log.Printf("[ERROR] Registry VersionCreate: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	version := allocateVersion()
	versionDir := filepath.Join(skillDir, version)

	if err := os.MkdirAll(versionDir, 0755); err != nil {
		log.Printf("[ERROR] Registry VersionCreate: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create version directory: "+err.Error()))
		return
	}

	// Optionally copy from active version
	if req.CopyFromActive && meta.ActiveVersion != "" {
		activeDir := filepath.Join(skillDir, meta.ActiveVersion)
		if err := copyDir(activeDir, versionDir); err != nil {
			os.RemoveAll(versionDir)
			log.Printf("[ERROR] Registry VersionCreate: copy from active: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to copy from active version: "+err.Error()))
			return
		}
	}

	now := time.Now().UnixNano()
	versionEntry := model.RegistryVersionEntry{
		Version:     version,
		Description: req.Description,
		CreatedAt:   now,
		Source:      "manual",
	}

	meta.Versions = append(meta.Versions, versionEntry)
	meta.UpdatedAt = now

	if err := writeRegistryMeta(skillDir, meta); err != nil {
		os.RemoveAll(versionDir)
		log.Printf("[ERROR] Registry VersionCreate: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistryVersionCreateResult{
		Version: versionEntry,
		Skill:   *meta,
	}))
}

// VersionGet retrieves a specific version's metadata and SKILLS.md content.
func (h *RegistryHandler) VersionGet(c *gin.Context) {
	var req model.RegistryVersionGetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := validateVersionID(req.Version); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
			return
		}
		log.Printf("[ERROR] Registry VersionGet: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	versionEntry, found := findVersionEntry(meta, req.Version)
	if !found {
		c.JSON(http.StatusNotFound, model.ErrResponse("version not found: "+req.Version))
		return
	}

	var frontmatter, body string
	versionDir := filepath.Join(skillDir, req.Version)
	if content, err := readSkillsMD(versionDir, req.Name); err == nil {
		frontmatter, body = splitFrontmatter(content)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistryVersionGetResult{
		Version:     *versionEntry,
		Frontmatter: frontmatter,
		Body:        body,
	}))
}

// VersionList lists all versions of a skill.
func (h *RegistryHandler) VersionList(c *gin.Context) {
	var req model.RegistryVersionListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
			return
		}
		log.Printf("[ERROR] Registry VersionList: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	versions := meta.Versions
	if versions == nil {
		versions = []model.RegistryVersionEntry{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistryVersionListResult{
		Versions:      versions,
		ActiveVersion: meta.ActiveVersion,
	}))
}

// VersionDelete deletes a version (cannot delete the active version).
func (h *RegistryHandler) VersionDelete(c *gin.Context) {
	var req model.RegistryVersionDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := validateVersionID(req.Version); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
			return
		}
		log.Printf("[ERROR] Registry VersionDelete: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	if meta.ActiveVersion == req.Version {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot delete the active version"))
		return
	}

	_, found := findVersionEntry(meta, req.Version)
	if !found {
		c.JSON(http.StatusNotFound, model.ErrResponse("version not found: "+req.Version))
		return
	}

	// Remove version directory
	versionDir := filepath.Join(skillDir, req.Version)
	if err := os.RemoveAll(versionDir); err != nil {
		log.Printf("[ERROR] Registry VersionDelete: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to delete version directory: "+err.Error()))
		return
	}

	// Remove from versions list
	newVersions := make([]model.RegistryVersionEntry, 0, len(meta.Versions))
	for _, v := range meta.Versions {
		if v.Version != req.Version {
			newVersions = append(newVersions, v)
		}
	}
	meta.Versions = newVersions
	meta.UpdatedAt = time.Now().UnixNano()

	if err := writeRegistryMeta(skillDir, meta); err != nil {
		log.Printf("[ERROR] Registry VersionDelete: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkMsg("version deleted: "+req.Version))
}

// maxTreeEntries limits the number of entries returned by VersionTree.
const maxTreeEntries = 10000

// VersionTree returns the directory tree of a specific version.
func (h *RegistryHandler) VersionTree(c *gin.Context) {
	var req model.RegistryVersionTreeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := validateVersionID(req.Version); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	versionDir := filepath.Join(h.mgr.RegistrySkillPath(req.Name), req.Version)
	if _, err := os.Stat(versionDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("version not found: "+req.Version))
		return
	}

	var files []model.SkillFileEntry
	var errLimitReached = fmt.Errorf("limit reached")
	walkErr := filepath.Walk(versionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(versionDir, path)
		if rel == "." {
			return nil
		}
		if len(files) >= maxTreeEntries {
			return errLimitReached
		}
		files = append(files, model.SkillFileEntry{
			Path:        rel,
			IsDirectory: info.IsDir(),
			Size:        info.Size(),
		})
		return nil
	})
	if walkErr != nil && walkErr != errLimitReached {
		log.Printf("[ERROR] Registry VersionTree: %v", walkErr)
	}

	if files == nil {
		files = []model.SkillFileEntry{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillTreeResult{
		Name:  req.Name,
		Files: files,
	}))
}

// ——— Version File Operations ———

func (h *RegistryHandler) resolveVersionPath(name, version, relPath string) (string, error) {
	if err := validateSkillID(name); err != nil {
		return "", err
	}
	if err := validateVersionID(version); err != nil {
		return "", err
	}
	versionDir := filepath.Join(h.mgr.RegistrySkillPath(name), version)
	if _, err := os.Stat(versionDir); err != nil {
		return "", fmt.Errorf("version not found: %s/%s", name, version)
	}
	return resolveSkillFilePath(versionDir, relPath)
}

// VersionFileRead reads a file within a specific version.
func (h *RegistryHandler) VersionFileRead(c *gin.Context) {
	var req model.RegistryVersionFileReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	resolved, err := h.resolveVersionPath(req.Name, req.Version, req.Path)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrResponse(err.Error()))
		return
	}

	content, err := os.ReadFile(resolved)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found: "+req.Path))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileReadResult{
		Path:    req.Path,
		Content: string(content),
	}))
}

// VersionFileWrite writes a file within a specific version.
func (h *RegistryHandler) VersionFileWrite(c *gin.Context) {
	var req model.RegistryVersionFileWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	resolved, err := h.resolveVersionPath(req.Name, req.Version, req.Path)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrResponse(err.Error()))
		return
	}

	if strings.EqualFold(filepath.Base(resolved), metaFile) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot write to system file: "+metaFile))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(resolved), 0755); err != nil {
		log.Printf("[ERROR] Registry VersionFileWrite: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create parent directory: "+err.Error()))
		return
	}

	data := []byte(req.Content)
	if err := os.WriteFile(resolved, data, 0644); err != nil {
		log.Printf("[ERROR] Registry VersionFileWrite: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileWriteResult{
		Path:         req.Path,
		BytesWritten: len(data),
	}))
}

// VersionFileUpdate replaces string content in a file within a specific version.
func (h *RegistryHandler) VersionFileUpdate(c *gin.Context) {
	var req model.RegistryVersionFileUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	resolved, err := h.resolveVersionPath(req.Name, req.Version, req.Path)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrResponse(err.Error()))
		return
	}

	if strings.EqualFold(filepath.Base(resolved), metaFile) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot update system file: "+metaFile))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	content, err := os.ReadFile(resolved)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found: "+req.Path))
		return
	}

	replacedCount := strings.Count(string(content), req.OldStr)
	if replacedCount > 0 {
		newContent := strings.ReplaceAll(string(content), req.OldStr, req.NewStr)
		if err := os.WriteFile(resolved, []byte(newContent), 0644); err != nil {
			log.Printf("[ERROR] Registry VersionFileUpdate: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
			return
		}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileUpdateResult{
		Path:          req.Path,
		ReplacedCount: replacedCount,
	}))
}

// VersionFileMkdir creates a directory within a specific version.
func (h *RegistryHandler) VersionFileMkdir(c *gin.Context) {
	var req model.RegistryVersionFileMkdirRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	resolved, err := h.resolveVersionPath(req.Name, req.Version, req.Path)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err := os.MkdirAll(resolved, 0755); err != nil {
		log.Printf("[ERROR] Registry VersionFileMkdir: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create directory: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileMkdirResult{Path: req.Path}))
}

// VersionFileDelete deletes a file or directory within a specific version.
func (h *RegistryHandler) VersionFileDelete(c *gin.Context) {
	var req model.RegistryVersionFileDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	resolved, err := h.resolveVersionPath(req.Name, req.Version, req.Path)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "not found") {
			status = http.StatusNotFound
		}
		c.JSON(status, model.ErrResponse(err.Error()))
		return
	}

	if strings.EqualFold(filepath.Base(resolved), metaFile) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot delete system file: "+metaFile))
		return
	}

	versionDir := filepath.Join(h.mgr.RegistrySkillPath(req.Name), req.Version)
	if resolved == filepath.Clean(versionDir) {
		c.JSON(http.StatusBadRequest, model.ErrResponse("cannot delete version root directory"))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if _, err := os.Lstat(resolved); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("path not found: "+req.Path))
		return
	}

	if err := os.RemoveAll(resolved); err != nil {
		log.Printf("[ERROR] Registry VersionFileDelete: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to delete: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SkillFileDeleteResult{Path: req.Path}))
}

// ——— Activate ———

// Activate deploys a specific version to /data/skills/.
func (h *RegistryHandler) Activate(c *gin.Context) {
	var req model.RegistryActivateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := validateVersionID(req.Version); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+req.Name))
			return
		}
		log.Printf("[ERROR] Registry Activate: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	versionEntry, found := findVersionEntry(meta, req.Version)
	if !found {
		c.JSON(http.StatusNotFound, model.ErrResponse("version not found: "+req.Version))
		return
	}

	versionDir := filepath.Join(skillDir, req.Version)
	if _, err := os.Stat(versionDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("version directory not found: "+req.Version))
		return
	}

	// Deploy to /data/skills/
	if err := h.deployVersionToSkills(skillDir, meta, versionEntry); err != nil {
		log.Printf("[ERROR] Registry Activate: deploy failed: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to deploy version: "+err.Error()))
		return
	}

	// Update registry meta
	meta.ActiveVersion = req.Version
	meta.UpdatedAt = time.Now().UnixNano()
	if err := writeRegistryMeta(skillDir, meta); err != nil {
		log.Printf("[ERROR] Registry Activate: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to update skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistryActivateResult{
		Skill:            *meta,
		ActivatedVersion: *versionEntry,
	}))
}

// ——— Commit ———

// Commit captures an agent's skill as a new version in the registry.
func (h *RegistryHandler) Commit(c *gin.Context) {
	var req model.RegistryCommitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	// Locate source from agent skills directory
	sourceDir := filepath.Join(h.mgr.SkillsRoot(req.AgentID), req.Name)
	if _, err := os.Stat(sourceDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("skill not found in agent workspace: "+req.Name))
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	skillDir := h.mgr.RegistrySkillPath(req.Name)

	// Auto-create registry entry if not exists
	var meta *model.RegistryMetaJSON
	if existingMeta, err := readRegistryMeta(skillDir); err == nil {
		meta = existingMeta
	} else {
		// Create new registry entry
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			log.Printf("[ERROR] Registry Commit: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create skill directory: "+err.Error()))
			return
		}

		now := time.Now().UnixNano()
		desc := req.Description
		// Try to extract description from source _meta.json
		if srcMeta, err := readSkillMeta(sourceDir); err == nil && srcMeta.Description != "" {
			desc = srcMeta.Description
		}
		meta = &model.RegistryMetaJSON{
			Name:          req.Name,
			Description:   desc,
			CreatedAt:     now,
			UpdatedAt:     now,
			ActiveVersion: "",
			Versions:      []model.RegistryVersionEntry{},
		}
		if err := writeRegistryMeta(skillDir, meta); err != nil {
			log.Printf("[ERROR] Registry Commit: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
			return
		}
	}

	// Allocate version
	version := allocateVersion()
	versionDir := filepath.Join(skillDir, version)

	if err := os.MkdirAll(versionDir, 0755); err != nil {
		log.Printf("[ERROR] Registry Commit: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create version directory: "+err.Error()))
		return
	}

	// Copy from agent skills, skipping _meta.json
	if err := copyDirSkippingMeta(sourceDir, versionDir); err != nil {
		os.RemoveAll(versionDir)
		log.Printf("[ERROR] Registry Commit: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to copy skill: "+err.Error()))
		return
	}

	now := time.Now().UnixNano()
	versionEntry := model.RegistryVersionEntry{
		Version:     version,
		Description: req.Description,
		CreatedAt:   now,
		Source:      "agent:" + req.AgentID,
	}

	meta.Versions = append(meta.Versions, versionEntry)
	meta.UpdatedAt = now

	// Optionally activate
	if req.Activate {
		if err := h.deployVersionToSkills(skillDir, meta, &versionEntry); err != nil {
			log.Printf("[ERROR] Registry Commit: deploy failed: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to activate version: "+err.Error()))
			return
		}
		meta.ActiveVersion = version
	}

	if err := writeRegistryMeta(skillDir, meta); err != nil {
		log.Printf("[ERROR] Registry Commit: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write skill metadata: "+err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistryCommitResult{
		Version: versionEntry,
		Skill:   *meta,
	}))
}

// copyDirSkippingMeta copies a directory but skips _meta.json files.
func copyDirSkippingMeta(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		// Skip _meta.json
		if rel == metaFile {
			return nil
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		// Use Lstat to detect symlinks that Walk may have resolved
		linfo, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		if linfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

// ——— Import ———

// Import imports a skill from a ZIP URL as a new version and auto-activates.
func (h *RegistryHandler) Import(c *gin.Context) {
	var req model.RegistryImportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if err := validateSkillID(req.Name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	if err := h.validateSkillURL(req.ZipURL); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("blocked URL: "+err.Error()))
		return
	}

	// Download ZIP outside the lock to avoid blocking other registry operations.
	resp, err := h.client().Get(req.ZipURL)
	if err != nil {
		log.Printf("[ERROR] Registry Import: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill: "+err.Error()))
		return
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to download skill: HTTP "+resp.Status))
		return
	}

	tmpFile, err := os.CreateTemp("", "registry-skill-*.zip")
	if err != nil {
		resp.Body.Close()
		log.Printf("[ERROR] Registry Import: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create temp file: "+err.Error()))
		return
	}
	tmpPath := tmpFile.Name()

	written, err := io.Copy(tmpFile, io.LimitReader(resp.Body, maxZipSize+1))
	resp.Body.Close()
	tmpFile.Close()

	if err != nil {
		os.Remove(tmpPath)
		log.Printf("[ERROR] Registry Import: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to save skill zip: "+err.Error()))
		return
	}
	if written > maxZipSize {
		os.Remove(tmpPath)
		c.JSON(http.StatusBadRequest, model.ErrResponse("skill zip exceeds size limit (100MB)"))
		return
	}

	// Lock only for the filesystem mutation phase.
	h.mu.Lock()
	result, err := h.importAsNewVersion(req.Name, "", tmpPath)
	h.mu.Unlock()
	os.Remove(tmpPath)
	if err != nil {
		log.Printf("[ERROR] Registry Import: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse(err.Error()))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(result))
}

// ImportUpload imports skills from uploaded ZIP files and auto-activates each.
func (h *RegistryHandler) ImportUpload(c *gin.Context) {
	maxUploadBody := int64(maxUploadFiles)*maxZipSize + 1024*1024
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUploadBody)

	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid multipart form: "+err.Error()))
		return
	}

	files := form.File["files"]
	names := form.Value["names"]

	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, model.ErrResponse("no files uploaded"))
		return
	}
	if len(files) > maxUploadFiles {
		c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("too many files (%d), max %d per request", len(files), maxUploadFiles)))
		return
	}
	if len(names) != len(files) {
		c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("names count (%d) must match files count (%d)", len(names), len(files))))
		return
	}

	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if err := validateSkillID(name); err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("invalid skill name %q: %s", name, err.Error())))
			return
		}
		if seen[name] {
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("duplicate skill name: %q", name)))
			return
		}
		seen[name] = true
	}

	results := make([]model.RegistryImportResult, 0, len(files))

	for i, fh := range files {
		skillName := names[i]

		if fh.Size > maxZipSize {
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("file %q exceeds size limit (100MB)", fh.Filename)))
			return
		}

		src, err := fh.Open()
		if err != nil {
			log.Printf("[ERROR] Registry ImportUpload: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse(fmt.Sprintf("failed to open uploaded file %q: %s", fh.Filename, err.Error())))
			return
		}

		tmpFile, err := os.CreateTemp("", "registry-upload-*.zip")
		if err != nil {
			src.Close()
			log.Printf("[ERROR] Registry ImportUpload: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create temp file: "+err.Error()))
			return
		}
		tmpPath := tmpFile.Name()

		written, err := io.Copy(tmpFile, io.LimitReader(src, maxZipSize+1))
		src.Close()
		tmpFile.Close()
		if err != nil {
			os.Remove(tmpPath)
			log.Printf("[ERROR] Registry ImportUpload: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse(fmt.Sprintf("failed to save uploaded file %q: %s", fh.Filename, err.Error())))
			return
		}
		if written > maxZipSize {
			os.Remove(tmpPath)
			c.JSON(http.StatusBadRequest, model.ErrResponse(fmt.Sprintf("file %q exceeds size limit (100MB)", fh.Filename)))
			return
		}

		h.mu.Lock()
		result, importErr := h.importAsNewVersion(skillName, "", tmpPath)
		h.mu.Unlock()
		os.Remove(tmpPath)

		if importErr != nil {
			log.Printf("[ERROR] Registry ImportUpload: %v", importErr)
			c.JSON(http.StatusInternalServerError, model.ErrResponse(fmt.Sprintf("failed to import %q: %s", fh.Filename, importErr.Error())))
			return
		}

		results = append(results, *result)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.RegistryImportUploadResult{Skills: results}))
}

// importAsNewVersion creates a new version from a ZIP file and auto-activates.
// Must be called with h.mu held.
func (h *RegistryHandler) importAsNewVersion(name, description, zipPath string) (*model.RegistryImportResult, error) {
	skillDir := h.mgr.RegistrySkillPath(name)

	// Auto-create registry entry if not exists
	var meta *model.RegistryMetaJSON
	if existingMeta, err := readRegistryMeta(skillDir); err == nil {
		meta = existingMeta
	} else {
		if err := os.MkdirAll(skillDir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create skill directory: %w", err)
		}
		now := time.Now().UnixNano()
		meta = &model.RegistryMetaJSON{
			Name:          name,
			Description:   description,
			CreatedAt:     now,
			UpdatedAt:     now,
			ActiveVersion: "",
			Versions:      []model.RegistryVersionEntry{},
		}
	}

	// Allocate version
	version := allocateVersion()
	versionDir := filepath.Join(skillDir, version)

	if err := os.MkdirAll(versionDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create version directory: %w", err)
	}

	// Extract ZIP
	if err := extractZip(zipPath, versionDir); err != nil {
		os.RemoveAll(versionDir)
		return nil, fmt.Errorf("failed to extract skill: %w", err)
	}

	now := time.Now().UnixNano()

	// Extract description from SKILLS.md if available
	desc := description
	if content, err := readSkillsMD(versionDir, name); err == nil {
		if d := extractDescriptionFromFrontmatter(content); d != "" && desc == "" {
			desc = d
		}
	} else {
		// Create default SKILLS.md
		defaultMD := fmt.Sprintf("---\nname: %s\n---\n", name)
		os.WriteFile(filepath.Join(versionDir, "SKILLS.md"), []byte(defaultMD), 0644)
	}

	versionEntry := model.RegistryVersionEntry{
		Version:     version,
		Description: desc,
		CreatedAt:   now,
		Source:      "import",
	}

	meta.Versions = append(meta.Versions, versionEntry)
	meta.UpdatedAt = now
	meta.ActiveVersion = version

	// Update skill description from version if empty
	if meta.Description == "" {
		meta.Description = desc
	}

	// Auto-activate: deploy to /data/skills/
	if err := h.deployVersionToSkills(skillDir, meta, &versionEntry); err != nil {
		return nil, fmt.Errorf("failed to deploy version: %w", err)
	}

	if err := writeRegistryMeta(skillDir, meta); err != nil {
		return nil, fmt.Errorf("failed to write skill metadata: %w", err)
	}

	return &model.RegistryImportResult{
		Version: versionEntry,
		Skill:   *meta,
	}, nil
}

// ——— Export ———

// Export exports a specific version (or active version) as a ZIP file.
func (h *RegistryHandler) Export(c *gin.Context) {
	name := c.Query("name")
	version := c.Query("version")

	if err := validateSkillID(name); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	skillDir := h.mgr.RegistrySkillPath(name)
	meta, err := readRegistryMeta(skillDir)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, model.ErrResponse("skill not found: "+name))
			return
		}
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read skill metadata: "+err.Error()))
		return
	}

	// Default to active version
	if version == "" {
		version = meta.ActiveVersion
	}
	if version == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("no active version and no version specified"))
		return
	}

	if err := validateVersionID(version); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	if _, found := findVersionEntry(meta, version); !found {
		c.JSON(http.StatusNotFound, model.ErrResponse("version not found: "+version))
		return
	}

	versionDir := filepath.Join(skillDir, version)
	if _, err := os.Stat(versionDir); err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("version directory not found: "+version))
		return
	}

	// Build ZIP
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	var totalSize int64
	if err := filepath.Walk(versionDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		linfo, lerr := os.Lstat(path)
		if lerr != nil {
			return lerr
		}
		if linfo.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(versionDir, path)
		if rel == "." {
			return nil
		}
		if info.IsDir() {
			_, err := zw.Create(rel + "/")
			return err
		}
		totalSize += info.Size()
		if totalSize > maxExtractedSize {
			return fmt.Errorf("export size exceeds limit (%dMB)", maxExtractedSize/1024/1024)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}); err != nil {
		log.Printf("[ERROR] Registry Export %s/%s: %v", name, version, err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to export skill: "+err.Error()))
		return
	}

	if err := zw.Close(); err != nil {
		log.Printf("[ERROR] Registry Export %s/%s: close zip: %v", name, version, err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to finalize zip: "+err.Error()))
		return
	}

	c.Header("Content-Type", "application/zip")
	// name and version are already validated to safe character sets, but use
	// url.PathEscape defensively in case validation rules are relaxed later.
	safeName := url.PathEscape(name) + "-" + url.PathEscape(version) + ".zip"
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, safeName))
	c.Writer.Write(buf.Bytes())
}

// ——— SSRF ———

func (h *RegistryHandler) validateSkillURL(rawURL string) error {
	if !h.ssrfProtection {
		return nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported scheme %q (only http/https allowed)", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty host in URL")
	}

	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("failed to resolve host %s: %w", host, err)
	}

	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("URL resolves to private/reserved IP %s", ip)
		}
	}

	return nil
}
