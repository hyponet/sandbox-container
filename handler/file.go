package handler

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
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

	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

type FileHandler struct {
	mgr     *session.Manager
	fileOp  executor.FileOperator
	isBwrap bool
}

var (
	errSkillsReadOnly       = errors.New("skills directory is read-only")
	errPathOutsideWriteRoot = errors.New("path escapes writable root")
	errGlobLimitReached     = errors.New("max results reached")
)

func NewFileHandler(mgr *session.Manager, fileOp executor.FileOperator, isBwrap bool) *FileHandler {
	return &FileHandler{mgr: mgr, fileOp: fileOp, isBwrap: isBwrap}
}

// fileOpOpts builds FileOpOptions from the resolved paths for a request.
func (h *FileHandler) fileOpOpts(agentID, sessionID string, agentWorkspace bool) executor.FileOpOptions {
	var opts executor.FileOpOptions
	if agentWorkspace {
		if h.isBwrap {
			opts.RWBinds = append(opts.RWBinds, executor.BindMount{Src: h.mgr.WorkspaceRoot(agentID), Dest: SandboxHome})
			opts.RWBinds = append(opts.RWBinds, executor.BindMount{Src: h.mgr.SkillsRoot(agentID), Dest: SandboxSkillsDir})
		} else {
			opts.RWBinds = append(opts.RWBinds, executor.BindMount{Src: h.mgr.WorkspaceRoot(agentID), Dest: h.mgr.WorkspaceRoot(agentID)})
			opts.RWBinds = append(opts.RWBinds, executor.BindMount{Src: h.mgr.SkillsRoot(agentID), Dest: h.mgr.SkillsRoot(agentID)})
		}
	} else {
		if h.isBwrap {
			opts.RWBinds = append(opts.RWBinds, executor.BindMount{Src: h.mgr.SessionRoot(agentID, sessionID), Dest: SandboxHome})
			opts.ROBinds = append(opts.ROBinds, executor.BindMount{Src: h.mgr.SkillsRoot(agentID), Dest: SandboxSkillsDir})
		} else {
			opts.RWBinds = append(opts.RWBinds, executor.BindMount{Src: h.mgr.SessionRoot(agentID, sessionID), Dest: h.mgr.SessionRoot(agentID, sessionID)})
			opts.ROBinds = append(opts.ROBinds, executor.BindMount{Src: h.mgr.SkillsRoot(agentID), Dest: h.mgr.SkillsRoot(agentID)})
		}
	}
	return opts
}

// toSandboxPath translates a host path to the sandbox-internal path for file operations.
func (h *FileHandler) toSandboxPath(agentID, sessionID, hostPath string, agentWorkspace bool) string {
	var hostRoot string
	if agentWorkspace {
		hostRoot = h.mgr.WorkspaceRoot(agentID)
	} else {
		hostRoot = h.mgr.SessionRoot(agentID, sessionID)
	}
	return hostToSandboxPath(h.isBwrap, hostRoot, h.mgr.SkillsRoot(agentID), hostPath)
}

// baseRootForDisplay returns the base directory for computing relative display paths.
func (h *FileHandler) baseRootForDisplay(agentID, sessionID string, agentWorkspace bool) string {
	if h.isBwrap {
		return SandboxHome
	}
	if agentWorkspace {
		return filepath.Clean(h.mgr.WorkspaceRoot(agentID))
	}
	return filepath.Clean(h.mgr.SessionRoot(agentID, sessionID))
}

func (h *FileHandler) shouldSkipImplicitSkillsPath(path string, isSkillsSearch bool) bool {
	if !h.isBwrap || isSkillsSearch {
		return false
	}

	cleanPath := filepath.Clean(path)
	cleanSkills := filepath.Clean(SandboxSkillsDir)
	return cleanPath == cleanSkills || strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanSkills+string(os.PathSeparator))
}

func (h *FileHandler) validateWritablePath(agentID, sessionID, realPath string, agentWorkspace bool) error {
	resolved, err := resolvePathThroughExistingSymlinks(realPath)
	if err != nil {
		return err
	}

	if !agentWorkspace && pathWithinRoots(resolved, h.mgr.SkillsRoot(agentID)) {
		return errSkillsReadOnly
	}

	allowedRoots := []string{filepath.Clean(h.mgr.SessionRoot(agentID, sessionID))}
	if agentWorkspace {
		allowedRoots = []string{
			filepath.Clean(h.mgr.WorkspaceRoot(agentID)),
			filepath.Clean(h.mgr.SkillsRoot(agentID)),
		}
	}
	if !pathWithinRoots(resolved, allowedRoots...) {
		return fmt.Errorf("%w: %s", errPathOutsideWriteRoot, realPath)
	}
	return nil
}

func resolvePathThroughExistingSymlinks(path string) (string, error) {
	cleanPath := filepath.Clean(path)
	current := cleanPath
	var suffix []string

	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("path has no existing parent: %s", path)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func pathWithinRoots(path string, roots ...string) bool {
	cleanPath := filepath.Clean(path)
	if resolvedPath, err := resolvePathThroughExistingSymlinks(path); err == nil {
		cleanPath = filepath.Clean(resolvedPath)
	}
	for _, root := range roots {
		cleanRoot := filepath.Clean(root)
		if resolvedRoot, err := resolvePathThroughExistingSymlinks(root); err == nil {
			cleanRoot = filepath.Clean(resolvedRoot)
		}
		if cleanPath == cleanRoot || strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func writePathStatus(err error) int {
	if errors.Is(err, errSkillsReadOnly) {
		return http.StatusForbidden
	}
	if errors.Is(err, errPathOutsideWriteRoot) {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
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

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)
	data, err := h.fileOp.ReadFile(c.Request.Context(), opts, sandboxPath)
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

	realPath, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, req.File, req.EnableAgentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := h.validateWritablePath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace); err != nil {
		c.JSON(writePathStatus(err), model.ErrResponse(err.Error()))
		return
	}
	if err := os.MkdirAll(filepath.Dir(realPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create parent directory: "+err.Error()))
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

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	ctx := c.Request.Context()
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)

	var written int
	if req.Append {
		n, err := h.fileOp.AppendFile(ctx, opts, sandboxPath, content, 0644)
		if err != nil {
			log.Printf("[ERROR] Write: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write: "+err.Error()))
			return
		}
		written = n
	} else {
		err = h.fileOp.WriteFile(ctx, opts, sandboxPath, content, 0644)
		if err != nil {
			log.Printf("[ERROR] Write: %v", err)
			c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
			return
		}
		written = len(content)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.FileWriteResult{
		File:         req.File,
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
	if err := h.validateWritablePath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace); err != nil {
		c.JSON(writePathStatus(err), model.ErrResponse(err.Error()))
		return
	}

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	ctx := c.Request.Context()
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)

	data, err := h.fileOp.ReadFile(ctx, opts, sandboxPath)
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
	if err := h.fileOp.WriteFile(ctx, opts, sandboxPath, []byte(newContent), 0644); err != nil {
		log.Printf("[ERROR] Replace: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
		return
	}

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

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)
	data, err := h.fileOp.ReadFile(c.Request.Context(), opts, sandboxPath)
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

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	var files []string
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)
	skillsRoot := h.mgr.SkillsRoot(req.AgentID)
	isSkillsSearch := strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || realPath == skillsRoot
	h.fileOp.Walk(c.Request.Context(), opts, sandboxPath, func(path string, info executor.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if h.shouldSkipImplicitSkillsPath(path, isSkillsSearch) {
			return nil
		}
		matched, _ := filepath.Match(req.Glob, filepath.Base(info.Name))
		if matched {
			rel, _ := filepath.Rel(sandboxPath, path)
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
	skillsRoot := h.mgr.SkillsRoot(req.AgentID)
	isSkillsSearch := strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || realPath == skillsRoot
	baseRoot := sessionRoot
	if isSkillsSearch {
		if h.isBwrap {
			baseRoot = SandboxSkillsDir
		} else {
			baseRoot = filepath.Clean(skillsRoot)
		}
	}

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	ctx := c.Request.Context()
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)

	h.fileOp.Walk(ctx, opts, sandboxPath, func(path string, info executor.FileInfo, walkErr error) error {
		if walkErr != nil || info.IsDir {
			return nil
		}
		if h.shouldSkipImplicitSkillsPath(path, isSkillsSearch) {
			return nil
		}

		// Check include/exclude patterns
		if !matchGlobFilters(path, req.Include, req.Exclude) {
			return nil
		}

		data, err := h.fileOp.ReadFile(ctx, opts, path)
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

	sessionRoot := h.baseRootForDisplay(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	skillsRoot := h.mgr.SkillsRoot(req.AgentID)
	isSkillsSearch := strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || realPath == skillsRoot
	baseRoot := sessionRoot
	if isSkillsSearch {
		if h.isBwrap {
			baseRoot = SandboxSkillsDir
		} else {
			baseRoot = filepath.Clean(skillsRoot)
		}
	}

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	ctx := c.Request.Context()
	pattern := normalizeGlobPattern(req.Pattern)
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)

	walkErr := h.fileOp.Walk(ctx, opts, sandboxPath, func(path string, info executor.FileInfo, walkErr error) error {
		if walkErr != nil || path == sandboxPath {
			return nil
		}
		if h.shouldSkipImplicitSkillsPath(path, isSkillsSearch) {
			return nil
		}

		relPath, err := filepath.Rel(sandboxPath, path)
		if err != nil {
			return nil
		}
		relPath = filepath.ToSlash(relPath)
		if !req.IncludeHidden && hasHiddenPathSegment(relPath) {
			return nil
		}
		if !matchGlobPattern(pattern, relPath) {
			return nil
		}
		if filesOnly && info.IsDir {
			return nil
		}

		displayRelPath, err := filepath.Rel(baseRoot, path)
		if err != nil {
			return nil
		}
		displayPath := "/" + displayRelPath
		if isSkillsSearch {
			displayPath = "/skills/" + displayRelPath
		}

		gfi := model.GlobFileInfo{
			Path:        displayPath,
			Name:        info.Name,
			IsDirectory: info.IsDir,
		}
		if includeMeta {
			size := info.Size
			modTime := info.ModTime.Format(time.RFC3339)
			gfi.Size = &size
			gfi.ModifiedTime = &modTime
		}
		files = append(files, gfi)
		if len(files) >= maxResults {
			truncated = true
			return errGlobLimitReached
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, errGlobLimitReached) {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to glob files: "+walkErr.Error()))
		return
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

	realPath, err := h.mgr.ResolvePathEx(agentID, sessionID, targetPath, agentWorkspace)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	if err := h.validateWritablePath(agentID, sessionID, realPath, agentWorkspace); err != nil {
		c.JSON(writePathStatus(err), model.ErrResponse(err.Error()))
		return
	}
	if err := os.MkdirAll(filepath.Dir(realPath), 0755); err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create parent directory: "+err.Error()))
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("file is required"))
		return
	}

	src, err := file.Open()
	if err != nil {
		log.Printf("[ERROR] Upload: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to open uploaded file"))
		return
	}
	defer src.Close()

	opts := h.fileOpOpts(agentID, sessionID, agentWorkspace)
	sandboxPath := h.toSandboxPath(agentID, sessionID, realPath, agentWorkspace)
	written, err := h.fileOp.CreateFile(c.Request.Context(), opts, sandboxPath, src)
	if err != nil {
		log.Printf("[ERROR] Upload: %v", err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to write file: "+err.Error()))
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

	opts := h.fileOpOpts(agentID, sessionID, agentWorkspace)
	ctx := c.Request.Context()
	sandboxPath := h.toSandboxPath(agentID, sessionID, realPath, agentWorkspace)

	info, err := h.fileOp.Stat(ctx, opts, sandboxPath)
	if err != nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("file not found"))
		return
	}

	if info.IsDir {
		c.JSON(http.StatusBadRequest, model.ErrResponse("path is a directory"))
		return
	}

	localPath, cleanup, err := h.fileOp.ServeFile(ctx, opts, sandboxPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read file: "+err.Error()))
		return
	}
	defer cleanup()

	c.Header("Content-Disposition", "attachment; filename="+filepath.Base(realPath))
	c.File(localPath)
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
	skillsRoot := h.mgr.SkillsRoot(req.AgentID)
	isSkillsSearch := strings.HasPrefix(realPath+string(os.PathSeparator), skillsRoot+string(os.PathSeparator)) || realPath == skillsRoot
	baseRoot := sessionRoot
	if isSkillsSearch {
		if h.isBwrap {
			baseRoot = SandboxSkillsDir
		} else {
			baseRoot = filepath.Clean(skillsRoot)
		}
	}

	opts := h.fileOpOpts(req.AgentID, req.SessionID, req.EnableAgentWorkspace)
	ctx := c.Request.Context()
	sandboxPath := h.toSandboxPath(req.AgentID, req.SessionID, realPath, req.EnableAgentWorkspace)

	if req.Recursive {
		h.fileOp.Walk(ctx, opts, sandboxPath, func(path string, info executor.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if path == sandboxPath {
				return nil
			}
			if h.shouldSkipImplicitSkillsPath(path, isSkillsSearch) {
				return nil
			}

			name := filepath.Base(info.Name)
			if !showHidden && strings.HasPrefix(name, ".") {
				return nil
			}

			// Skip the skills symlink itself (it's handled as a virtual path)
			if name == "skills" && info.Mode&os.ModeSymlink != 0 {
				return nil
			}

			// Check file type filter
			if len(req.FileTypes) > 0 && !info.IsDir {
				ext := strings.ToLower(filepath.Ext(name))
				matched := false
				for _, ft := range req.FileTypes {
					if strings.ToLower(ft) == ext || (len(ext) > 0 && strings.ToLower(ft) == ext[1:]) {
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
				IsDirectory: info.IsDir,
			}
			if includeSize {
				size := info.Size
				fi.Size = &size
			}
			if includePerms {
				perm := info.Mode.String()
				fi.Permissions = &perm
			}
			modTime := info.ModTime.Format(time.RFC3339)
			fi.ModifiedTime = &modTime
			if !info.IsDir {
				ext := filepath.Ext(name)
				if ext != "" {
					fi.Extension = &ext
				}
			}

			files = append(files, fi)
			if info.IsDir {
				dirCount++
			} else {
				fileCount++
			}
			return nil
		})
	} else {
		entries, err := h.fileOp.ReadDir(ctx, opts, sandboxPath)
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
			name := entry.Name
			if !showHidden && strings.HasPrefix(name, ".") {
				continue
			}

			// Follow symlinks: use Stat to resolve link targets
			fullPath := filepath.Join(sandboxPath, name)
			info, err := h.fileOp.Stat(ctx, opts, fullPath)
			if err != nil {
				continue
			}
			isDir := info.IsDir

			// Check file type filter
			if len(req.FileTypes) > 0 && !isDir {
				ext := strings.ToLower(filepath.Ext(name))
				matched := false
				for _, ft := range req.FileTypes {
					if strings.ToLower(ft) == ext || (len(ext) > 0 && strings.ToLower(ft) == ext[1:]) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}

			relPath, _ := filepath.Rel(baseRoot, filepath.Join(sandboxPath, name))
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
				size := info.Size
				fi.Size = &size
			}
			if includePerms {
				perm := info.Mode.String()
				fi.Permissions = &perm
			}
			modTime := info.ModTime.Format(time.RFC3339)
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

// normalizeGlobPattern canonicalizes the API glob pattern to slash-separated form.
func normalizeGlobPattern(pattern string) string {
	clean := filepath.ToSlash(strings.TrimSpace(pattern))
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "*"
	}
	return clean
}

func hasHiddenPathSegment(relPath string) bool {
	for _, segment := range strings.Split(filepath.ToSlash(relPath), "/") {
		if strings.HasPrefix(segment, ".") && segment != "." && segment != ".." {
			return true
		}
	}
	return false
}

func matchGlobPattern(pattern, relPath string) bool {
	patternSegments := strings.Split(filepath.ToSlash(pattern), "/")
	pathSegments := strings.Split(filepath.ToSlash(relPath), "/")
	return matchGlobSegments(patternSegments, pathSegments)
}

func matchGlobSegments(patternSegments, pathSegments []string) bool {
	if len(patternSegments) == 0 {
		return len(pathSegments) == 0
	}
	if patternSegments[0] == "**" {
		for i := 0; i <= len(pathSegments); i++ {
			if matchGlobSegments(patternSegments[1:], pathSegments[i:]) {
				return true
			}
		}
		return false
	}
	if len(pathSegments) == 0 {
		return false
	}
	matched, err := filepath.Match(patternSegments[0], pathSegments[0])
	if err != nil || !matched {
		return false
	}
	return matchGlobSegments(patternSegments[1:], pathSegments[1:])
}

// used for testing
var _ = sort.Sort
var _ = regexp.Compile
var _ = bytes.NewReader
var _ = strconv.Itoa
var _ = exec.Command
