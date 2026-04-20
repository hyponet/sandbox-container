package handler

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

type FileHandler struct {
	mgr *session.Manager
}

func NewFileHandler(mgr *session.Manager) *FileHandler {
	return &FileHandler{mgr: mgr}
}

// baseRootForDisplay returns the base directory for computing relative display paths.
func (h *FileHandler) baseRootForDisplay(agentID, sessionID string, agentWorkspace bool) string {
	if agentWorkspace {
		return filepath.Clean(h.mgr.WorkspaceRoot(agentID))
	}
	return filepath.Clean(h.mgr.SessionRoot(agentID, sessionID))
}

// ---- Read ----

func (h *FileHandler) Read(c *gin.Context) {
	var req model.FileReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.File, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	data, err := os.ReadFile(realPath)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found: "+err.Error()))
		return
	}

	lines := strings.Split(string(data), "\n")
	if req.StartLine != nil || req.EndLine != nil {
		start := 0
		end := len(lines)
		if req.StartLine != nil && *req.StartLine >= 0 && *req.StartLine < end {
			start = *req.StartLine
		}
		if req.EndLine != nil && *req.EndLine >= 0 && *req.EndLine < end {
			end = *req.EndLine
		}
		lines = lines[start:end]
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileReadResult{
		Content: strings.Join(lines, "\n"),
		File:    req.File,
	}))
}

// ---- Write ----

func (h *FileHandler) Write(c *gin.Context) {
	var req model.FileWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if session.IsSkillsPath(req.File) && !req.EnableAgentWorkspace {
		c.JSON(http.StatusForbidden, model.ErrResponse("skills directory is read-only"))
		return
	}

	// Ensure parent directory exists
	if err := h.mgr.EnsureParentDirEx(req.AgentID, req.SessionID, req.File, req.EnableAgentWorkspace); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.File, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	var content []byte
	switch req.Encoding {
	case "base64":
		content, err = base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse("invalid base64: "+err.Error()))
			return
		}
	default:
		content = []byte(req.Content)
		if req.LeadingNewline {
			content = append([]byte("\n"), content...)
		}
		if req.TrailingNewline {
			content = append(content, '\n')
		}
	}

	var written int
	if req.Append {
		f, err := os.OpenFile(realPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Printf("[ERROR] Write: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to open file: "+err.Error()))
			return
		}
		defer f.Close()
		n, err := f.Write(content)
		if err != nil {
			log.Printf("[ERROR] Write: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write: "+err.Error()))
			return
		}
		written = n
	} else {
		err = os.WriteFile(realPath, content, 0644)
		if err != nil {
			log.Printf("[ERROR] Write: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
			return
		}
		written = len(content)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileWriteResult{
		File:        req.File,
		BytesWritten: &written,
	}))
}

// ---- Replace ----

func (h *FileHandler) Replace(c *gin.Context) {
	var req model.FileReplaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	if session.IsSkillsPath(req.File) && !req.EnableAgentWorkspace {
		c.JSON(http.StatusForbidden, model.ErrResponse("skills directory is read-only"))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.File, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	data, err := os.ReadFile(realPath)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found: "+err.Error()))
		return
	}

	content := string(data)
	count := strings.Count(content, req.OldStr)
	if count == 0 {
		c.JSON(http.StatusOK, model.OkResponse(model.FileReplaceResult{
			File:          req.File,
			ReplacedCount: 0,
		}))
		return
	}

	newContent := strings.ReplaceAll(content, req.OldStr, req.NewStr)
	os.WriteFile(realPath, []byte(newContent), 0644)

	c.JSON(http.StatusOK, model.OkResponse(model.FileReplaceResult{
		File:          req.File,
		ReplacedCount: count,
	}))
}

// ---- Search ----

func (h *FileHandler) Search(c *gin.Context) {
	var req model.FileSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.File, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	re, err := regexp.Compile(req.Regex)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid regex: "+err.Error()))
		return
	}

	data, err := os.ReadFile(realPath)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found: "+err.Error()))
		return
	}

	var matches []string
	var lineNumbers []int
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if re.MatchString(line) {
			matches = append(matches, line)
			lineNumbers = append(lineNumbers, i+1)
		}
	}

	if matches == nil {
		matches = []string{}
	}
	if lineNumbers == nil {
		lineNumbers = []int{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileSearchResult{
		File:        req.File,
		Matches:     matches,
		LineNumbers: lineNumbers,
	}))
}

// ---- Find ----

func (h *FileHandler) Find(c *gin.Context) {
	var req model.FileFindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.Path, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	var files []string
	filepath.Walk(realPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		matched, _ := filepath.Match(req.Glob, info.Name())
		if matched {
			rel, _ := filepath.Rel(realPath, path)
			files = append(files, filepath.Join(req.Path, rel))
		}
		return nil
	})

	if files == nil {
		files = []string{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileFindResult{
		Path:  req.Path,
		Files: files,
	}))
}

// ---- Grep ----

func (h *FileHandler) Grep(c *gin.Context) {
	var req model.FileGrepRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.Path, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 500
	}

	pattern := req.Pattern
	if req.FixedStrings {
		pattern = regexp.QuoteMeta(pattern)
	}
	flags := ""
	if req.CaseInsensitive {
		flags = "(?i)"
	}
	re, err := regexp.Compile(flags + pattern)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid pattern: "+err.Error()))
		return
	}

	var matches []model.GrepMatch
	truncated := false

	// Determine the base root for relative path display
	sessionRoot := h.baseRootForDisplay(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	// If searching in skills, use skills root for relative paths
	skillsRoot := filepath.Clean(h.mgr.SkillsRoot(req.AgentID))
	isSkillsSearch := strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || realPath == skillsRoot
	baseRoot := sessionRoot
	if isSkillsSearch {
		baseRoot = skillsRoot
	}

	filepath.Walk(realPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir() {
			return nil
		}

		// Check include/exclude patterns
		if !matchGlobFilters(path, req.Include, req.Exclude) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(baseRoot, path)
		displayPath := "/" + relPath
		if isSkillsSearch {
			displayPath = "/skills/" + relPath
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				m := model.GrepMatch{
					File:        displayPath,
					LineNumber:  i + 1,
					LineContent: line,
				}
				if req.ContextBefore > 0 && i > 0 {
					start := i - req.ContextBefore
					if start < 0 {
						start = 0
					}
					m.ContextBefore = lines[start:i]
				}
				if req.ContextAfter > 0 && i < len(lines)-1 {
					end := i + 1 + req.ContextAfter
					if end > len(lines) {
						end = len(lines)
					}
					m.ContextAfter = lines[i+1 : end]
				}
				matches = append(matches, m)
				if len(matches) >= maxResults {
					truncated = true
					return fmt.Errorf("max results reached")
				}
			}
		}
		return nil
	})

	if matches == nil {
		matches = []model.GrepMatch{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileGrepResult{
		Path:       req.Path,
		Pattern:    req.Pattern,
		Matches:    matches,
		MatchCount: len(matches),
		Truncated:  truncated,
	}))
}

// ---- Glob ----

func (h *FileHandler) Glob(c *gin.Context) {
	var req model.FileGlobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.Path, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 5000
	}

	includeMeta := true
	if req.IncludeMetadata != nil {
		includeMeta = *req.IncludeMetadata
	}
	filesOnly := true
	if req.FilesOnly != nil {
		filesOnly = *req.FilesOnly
	}

	var files []model.GlobFileInfo
	truncated := false

	pattern := filepath.Join(realPath, req.Pattern)
	matches, _ := filepath.Glob(pattern)

	if len(matches) == 0 && strings.Contains(req.Pattern, "**") {
		matches = globWalk(realPath, req.Pattern, req.IncludeHidden)
	}

	sessionRoot := h.baseRootForDisplay(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	skillsRoot := filepath.Clean(h.mgr.SkillsRoot(req.AgentID))
	isSkillsSearch := strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || realPath == skillsRoot
	baseRoot := sessionRoot
	if isSkillsSearch {
		baseRoot = skillsRoot
	}

	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			continue
		}
		if filesOnly && info.IsDir() {
			continue
		}

		relPath, _ := filepath.Rel(baseRoot, m)
		displayPath := "/" + relPath
		if isSkillsSearch {
			displayPath = "/skills/" + relPath
		}

		gfi := model.GlobFileInfo{
			Path:        displayPath,
			Name:        info.Name(),
			IsDirectory: info.IsDir(),
		}
		if includeMeta {
			size := info.Size()
			modTime := info.ModTime().Format(time.RFC3339)
			gfi.Size = &size
			gfi.ModifiedTime = &modTime
		}
		files = append(files, gfi)

		if len(files) >= maxResults {
			truncated = true
			break
		}
	}

	if files == nil {
		files = []model.GlobFileInfo{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileGlobResult{
		Path:       req.Path,
		Pattern:    req.Pattern,
		Files:      files,
		TotalCount: len(files),
		Truncated:  truncated,
	}))
}

// ---- Upload ----

func (h *FileHandler) Upload(c *gin.Context) {
	agentID := c.PostForm("agent_id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("agent_id is required"))
		return
	}
	sessionID := c.PostForm("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("session_id is required"))
		return
	}
	targetPath := c.PostForm("path")
	if targetPath == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("path is required"))
		return
	}

	agentWorkspace := c.PostForm("enable_agent_workspace") == "true"

	if session.IsSkillsPath(targetPath) && !agentWorkspace {
		c.JSON(http.StatusForbidden, model.ErrResponse("skills directory is read-only"))
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("file is required"))
		return
	}

	if err := h.mgr.EnsureParentDirEx(agentID, sessionID, targetPath, agentWorkspace); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(agentID, sessionID, targetPath, agentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	src, err := file.Open()
	if err != nil {
		log.Printf("[ERROR] Upload: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to open uploaded file"))
		return
	}
	defer src.Close()

	dst, err := os.Create(realPath)
	if err != nil {
		log.Printf("[ERROR] Upload: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create file"))
		return
	}
	defer dst.Close()

	written, err := ioCopyBuffer(dst, src)
	if err != nil {
		log.Printf("[ERROR] Upload: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file"))
		return
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileUploadResult{
		FilePath: targetPath,
		FileSize: written,
		Success:  true,
	}))
}

// ---- Download ----

func (h *FileHandler) Download(c *gin.Context) {
	agentID := c.Query("agent_id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("agent_id is required"))
		return
	}
	sessionID := c.Query("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("session_id is required"))
		return
	}
	filePath := c.Query("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("path is required"))
		return
	}

	agentWorkspace := c.Query("enable_agent_workspace") == "true"

	realPath, err := h.mgr.ResolvePathEx(agentID, sessionID, filePath, agentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	info, err := os.Stat(realPath)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found"))
		return
	}

	if info.IsDir() {
		c.JSON(http.StatusBadRequest, model.ErrResponse("path is a directory"))
		return
	}

	c.Header("Content-Disposition", "attachment; filename="+filepath.Base(realPath))
	c.File(realPath)
}

// ---- List ----

func (h *FileHandler) List(c *gin.Context) {
	var req model.FileListRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.Path, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	showHidden := true
	if req.ShowHidden != nil {
		showHidden = *req.ShowHidden
	}
	includeSize := true
	if req.IncludeSize != nil {
		includeSize = *req.IncludeSize
	}
	includePerms := false
	if req.IncludePermissions != nil {
		includePerms = *req.IncludePermissions
	}

	var files []model.FileInfo
	fileCount := 0
	dirCount := 0

	// Determine base root for relative paths
	sessionRoot := h.baseRootForDisplay(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	skillsRoot := filepath.Clean(h.mgr.SkillsRoot(req.AgentID))
	isSkillsSearch := strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || realPath == skillsRoot
	baseRoot := sessionRoot
	if isSkillsSearch {
		baseRoot = skillsRoot
	}

	if req.Recursive {
		filepath.Walk(realPath, func(path string, info fs.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == realPath {
				return nil
			}

			name := info.Name()
			if !showHidden && strings.HasPrefix(name, ".") {
				return nil
			}

			// Skip the skills symlink itself (it's handled as a virtual path)
			if name == "skills" && isSymlink(path) {
				return nil
			}

			// Check file type filter
			if len(req.FileTypes) > 0 && !info.IsDir() {
				ext := strings.ToLower(filepath.Ext(name))
				matched := false
				for _, ft := range req.FileTypes {
					if strings.ToLower(ft) == ext || strings.ToLower(ft) == ext[1:] {
						matched = true
						break
					}
				}
				if !matched {
					return nil
				}
			}

			relPath, _ := filepath.Rel(baseRoot, path)
			displayPath := "/" + relPath
			if isSkillsSearch {
				displayPath = "/skills/" + relPath
			}

			fi := model.FileInfo{
				Name:        name,
				Path:        displayPath,
				IsDirectory: info.IsDir(),
			}
			if includeSize {
				size := info.Size()
				fi.Size = &size
			}
			if includePerms {
				perm := info.Mode().String()
				fi.Permissions = &perm
			}
			modTime := info.ModTime().Format(time.RFC3339)
			fi.ModifiedTime = &modTime
			if !info.IsDir() {
				ext := filepath.Ext(name)
				if ext != "" {
					fi.Extension = &ext
				}
			}

			files = append(files, fi)
			if info.IsDir() {
				dirCount++
			} else {
				fileCount++
			}
			return nil
		})
	} else {
		entries, err := os.ReadDir(realPath)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusOK, model.OkResponse(model.FileListResult{
					Path:  req.Path,
					Files: []model.FileInfo{},
				}))
				return
			}
			log.Printf("[ERROR] List: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to list directory: "+err.Error()))
			return
		}

		for _, entry := range entries {
			name := entry.Name()
			if !showHidden && strings.HasPrefix(name, ".") {
				continue
			}

			// Follow symlinks: use os.Stat to resolve link targets
		 fullPath := filepath.Join(realPath, name)
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			isDir := info.IsDir()

			// Check file type filter
			if len(req.FileTypes) > 0 && !isDir {
				ext := strings.ToLower(filepath.Ext(name))
				matched := false
				for _, ft := range req.FileTypes {
					if strings.ToLower(ft) == ext || strings.ToLower(ft) == ext[1:] {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}

			relPath, _ := filepath.Rel(baseRoot, filepath.Join(realPath, name))
			displayPath := "/" + relPath
			if isSkillsSearch {
				displayPath = "/skills/" + relPath
			}

			fi := model.FileInfo{
				Name:        name,
				Path:        displayPath,
				IsDirectory: isDir,
			}
			if includeSize {
				size := info.Size()
				fi.Size = &size
			}
			if includePerms {
				perm := info.Mode().String()
				fi.Permissions = &perm
			}
			modTime := info.ModTime().Format(time.RFC3339)
			fi.ModifiedTime = &modTime
			if !isDir {
				ext := filepath.Ext(name)
				if ext != "" {
					fi.Extension = &ext
				}
			}

			files = append(files, fi)
			if isDir {
				dirCount++
			} else {
				fileCount++
			}
		}
	}

	if files == nil {
		files = []model.FileInfo{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileListResult{
		Path:           req.Path,
		Files:          files,
		TotalCount:     len(files),
		DirectoryCount: dirCount,
		FileCount:      fileCount,
	}))
}

// ---- Helpers ----

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

func matchGlobFilters(path string, include, exclude []string) bool {
	if len(exclude) > 0 {
		for _, ex := range exclude {
			if matched, _ := filepath.Match(ex, filepath.Base(path)); matched {
				return false
			}
		}
	}
	if len(include) > 0 {
		matched := false
		for _, inc := range include {
			if m, _ := filepath.Match(inc, filepath.Base(path)); m {
				matched = true
				break
			}
		}
		return matched
	}
	return true
}

// globWalk does a recursive walk to match ** patterns.
func globWalk(root, pattern string, includeHidden bool) []string {
	var results []string
	suffix := strings.TrimPrefix(pattern, "**/")
	if suffix == "" {
		suffix = "*"
	}

	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !includeHidden && strings.HasPrefix(info.Name(), ".") {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if suffix != "" {
			matched, _ := filepath.Match(suffix, info.Name())
			if matched {
				results = append(results, path)
			}
		}
		return nil
	})

	return results
}

func ioCopyBuffer(dst *os.File, src multipartFile) (int64, error) {
	return copyBuffer(dst, bufio.NewReader(src))
}

type multipartFile interface {
	Read(p []byte) (int, error)
}

func copyBuffer(dst *os.File, src *bufio.Reader) (int64, error) {
	var written int64
	buf := make([]byte, 32*1024)
	for {
		nr, err := src.Read(buf)
		if nr > 0 {
			nw, err := dst.Write(buf[:nr])
			if err != nil {
				return written, err
			}
			written += int64(nw)
		}
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return written, err
		}
	}
	return written, nil
}

// used for testing
var _ = sort.Sort
var _ = regexp.Compile
var _ = bytes.NewReader
var _ = strconv.Itoa
var _ = exec.Command
