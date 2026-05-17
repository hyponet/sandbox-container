package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/projectdata"
	"github.com/hyponet/sandbox-container/session"
	"github.com/hyponet/sandbox-container/userdata"

	"github.com/gin-gonic/gin"
)

func setupRouter() (*gin.Engine, *session.Manager) {
	return setupRouterWithFileOperator(newWritableVirtualFileOperator(nil))
}

func setupRouterWithFileOperator(fileOp executor.FileOperator) (*gin.Engine, *session.Manager) {
	gin.SetMode(gin.TestMode)
	dir := tTempDir()
	mgr := session.NewManager(dir, 24*time.Hour)
	udMgr := userdata.NewManager(filepath.Join(dir, "users"))
	pdMgr := projectdata.NewManager(filepath.Join(dir, "projects"))

	r := gin.New()

	fileH := NewFileHandler(mgr, udMgr, pdMgr, fileOp)
	f := r.Group("/v1/file")
	{
		f.POST("/read", fileH.Read)
		f.POST("/write", fileH.Write)
		f.POST("/replace", fileH.Replace)
		f.POST("/search", fileH.Search)
		f.POST("/find", fileH.Find)
		f.POST("/grep", fileH.Grep)
		f.POST("/glob", fileH.Glob)
		f.POST("/list", fileH.List)
		f.GET("/download", fileH.Download)
		f.POST("/upload", fileH.Upload)
	}

	return r, mgr
}

func tTempDir() string {
	dir := filepath.Join(os.TempDir(), "sandbox-test-"+fmt.Sprintf("%d", time.Now().UnixNano()))
	os.MkdirAll(dir, 0755)
	return dir
}

type virtualFileOperator struct {
	// Per-namespace stores: namespace = first RWBind Src (session/workspace root)
	nsFiles map[string]map[string]string
	nsDirs  map[string]map[string]struct{}
}

func newVirtualFileOperator(files map[string]string) *virtualFileOperator {
	return newWritableVirtualFileOperator(files)
}

func newWritableVirtualFileOperator(files map[string]string) *virtualFileOperator {
	v := &virtualFileOperator{
		nsFiles: make(map[string]map[string]string),
		nsDirs:  make(map[string]map[string]struct{}),
	}

	// Empty namespace for pre-seeded files
	v.nsFiles[""] = make(map[string]string)
	v.nsDirs[""] = map[string]struct{}{filepath.Clean(SandboxHome): {}}

	if files != nil {
		for path, content := range files {
			cleanPath := filepath.Clean(path)
			v.nsFiles[""][cleanPath] = content
			for dir := filepath.Dir(cleanPath); ; dir = filepath.Dir(dir) {
				v.nsDirs[""][filepath.Clean(dir)] = struct{}{}
				if dir == "/" || dir == filepath.Dir(dir) {
					break
				}
			}
		}
	}

	return v
}

func (v *virtualFileOperator) getNS(opts executor.FileOpOptions) string {
	if len(opts.RWBinds) > 0 {
		return opts.RWBinds[0].Src
	}
	return ""
}

func (v *virtualFileOperator) filesForNS(ns string) map[string]string {
	if f, ok := v.nsFiles[ns]; ok {
		return f
	}
	f := make(map[string]string)
	v.nsFiles[ns] = f
	return f
}

func (v *virtualFileOperator) dirsForNS(ns string) map[string]struct{} {
	if d, ok := v.nsDirs[ns]; ok {
		return d
	}
	d := map[string]struct{}{filepath.Clean(SandboxHome): {}}
	v.nsDirs[ns] = d
	return d
}

// hostPathForSandbox maps a sandbox path to a host path using the bind mounts in opts.
func hostPathForSandbox(opts executor.FileOpOptions, sandboxPath string) string {
	cleanPath := filepath.Clean(sandboxPath)
	for _, bind := range opts.ROBinds {
		cleanDest := filepath.Clean(bind.Dest)
		if cleanPath == cleanDest || strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanDest+string(os.PathSeparator)) {
			return filepath.Join(bind.Src, strings.TrimPrefix(cleanPath, cleanDest))
		}
	}
	for _, bind := range opts.RWBinds {
		cleanDest := filepath.Clean(bind.Dest)
		if cleanPath == cleanDest || strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanDest+string(os.PathSeparator)) {
			return filepath.Join(bind.Src, strings.TrimPrefix(cleanPath, cleanDest))
		}
	}
	return ""
}

func hasBindForDest(opts executor.FileOpOptions, dest string) bool {
	cleanDest := filepath.Clean(dest)
	for _, bind := range opts.RWBinds {
		if filepath.Clean(bind.Dest) == cleanDest {
			return true
		}
	}
	return false
}

func isSharedSandboxPath(opts executor.FileOpOptions, cleanPath string) bool {
	cleanProjectdata := filepath.Clean(SandboxProjectdataDir)
	if strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanProjectdata+string(os.PathSeparator)) {
		return hasBindForDest(opts, SandboxProjectdataDir)
	}

	cleanHome := filepath.Clean(SandboxHome)
	if strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanHome+string(os.PathSeparator)) {
		return hasBindForDest(opts, SandboxHome)
	}

	return false
}

func (v *virtualFileOperator) ReadFile(_ context.Context, opts executor.FileOpOptions, path string) ([]byte, error) {
	ns := v.getNS(opts)
	cleanPath := filepath.Clean(path)
	// Check current namespace first
	if files := v.nsFiles[ns]; files != nil {
		if content, ok := files[cleanPath]; ok {
			return []byte(content), nil
		}
	}
	if isSharedSandboxPath(opts, cleanPath) {
		for otherNS, files := range v.nsFiles {
			if otherNS == ns {
				continue
			}
			if content, ok := files[cleanPath]; ok {
				return []byte(content), nil
			}
		}
	}
	// Fallback to empty namespace (pre-seeded files)
	if ns != "" {
		if content, ok := v.nsFiles[""][cleanPath]; ok {
			return []byte(content), nil
		}
	}
	// Fallback: read from host filesystem via bind mount mapping
	if hostPath := hostPathForSandbox(opts, cleanPath); hostPath != "" {
		if data, err := os.ReadFile(hostPath); err == nil {
			return data, nil
		}
	}
	return nil, os.ErrNotExist
}

func (v *virtualFileOperator) WriteFile(_ context.Context, opts executor.FileOpOptions, path string, data []byte, _ os.FileMode) error {
	ns := v.getNS(opts)
	files := v.filesForNS(ns)
	dirs := v.dirsForNS(ns)
	cleanPath := filepath.Clean(path)
	files[cleanPath] = string(data)
	for dir := filepath.Dir(cleanPath); ; dir = filepath.Dir(dir) {
		dirs[filepath.Clean(dir)] = struct{}{}
		if dir == "/" || dir == filepath.Dir(dir) {
			break
		}
	}
	return nil
}

func (v *virtualFileOperator) AppendFile(_ context.Context, opts executor.FileOpOptions, path string, data []byte, _ os.FileMode) (int, error) {
	ns := v.getNS(opts)
	files := v.filesForNS(ns)
	cleanPath := filepath.Clean(path)
	files[cleanPath] += string(data)
	return len(data), nil
}

func (v *virtualFileOperator) Stat(_ context.Context, opts executor.FileOpOptions, path string) (*executor.FileInfo, error) {
	info, ok := v.infoForPath(opts, path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return &info, nil
}

func (v *virtualFileOperator) Lstat(ctx context.Context, opts executor.FileOpOptions, path string) (*executor.FileInfo, error) {
	return v.Stat(ctx, opts, path)
}

func (v *virtualFileOperator) ReadDir(_ context.Context, opts executor.FileOpOptions, path string) ([]executor.FileInfo, error) {
	ns := v.getNS(opts)
	cleanRoot := filepath.Clean(path)

	// Collect children from all namespaces (ns-specific, empty ns, host fs)
	children := map[string]executor.FileInfo{}

	// Check namespace-specific dirs
	if dirs := v.nsDirs[ns]; dirs != nil {
		if _, ok := dirs[cleanRoot]; ok {
			for dir := range dirs {
				if dir == cleanRoot || filepath.Dir(dir) != cleanRoot {
					continue
				}
				if info, ok := v.infoForPathInNS(ns, dir); ok {
					children[dir] = info
				}
			}
		}
		// Also add files
		if files := v.nsFiles[ns]; files != nil {
			for filePath := range files {
				if filepath.Dir(filePath) != cleanRoot {
					continue
				}
				if info, ok := v.infoForPathInNS(ns, filePath); ok {
					children[filePath] = info
				}
			}
		}
	}

	// Also include entries from empty namespace (pre-seeded)
	if ns != "" {
		if dirs := v.nsDirs[""]; dirs != nil {
			if _, ok := dirs[cleanRoot]; ok {
				for dir := range dirs {
					if filepath.Dir(dir) == cleanRoot {
						if _, exists := children[dir]; !exists {
							if info, ok := v.infoForPathInNS("", dir); ok {
								children[dir] = info
							}
						}
					}
				}
			}
			if files := v.nsFiles[""]; files != nil {
				for filePath := range files {
					if filepath.Dir(filePath) == cleanRoot {
						if _, exists := children[filePath]; !exists {
							if info, ok := v.infoForPathInNS("", filePath); ok {
								children[filePath] = info
							}
						}
					}
				}
			}
		}
	}

	// Also include host filesystem entries via bind mounts
	for _, bind := range opts.ROBinds {
		cleanDest := filepath.Clean(bind.Dest)
		if cleanRoot == cleanDest || strings.HasPrefix(cleanRoot+string(os.PathSeparator), cleanDest+string(os.PathSeparator)) {
			rel := strings.TrimPrefix(cleanRoot, cleanDest)
			hostDir := filepath.Join(bind.Src, rel)
			if entries, err := os.ReadDir(hostDir); err == nil {
				for _, e := range entries {
					name := e.Name()
					key := filepath.Join(cleanRoot, name)
					if _, exists := children[key]; !exists {
						info, _ := e.Info()
						if info != nil {
							children[key] = executor.FileInfo{
								Name:    name,
								Size:    info.Size(),
								Mode:    info.Mode(),
								ModTime: info.ModTime(),
								IsDir:   e.IsDir(),
							}
						}
					}
				}
			}
		}
	}

	if len(children) == 0 {
		return nil, os.ErrNotExist
	}

	var paths []string
	for child := range children {
		paths = append(paths, child)
	}
	sort.Strings(paths)

	entries := make([]executor.FileInfo, 0, len(paths))
	for _, child := range paths {
		entries = append(entries, children[child])
	}
	return entries, nil
}

func (v *virtualFileOperator) Walk(_ context.Context, opts executor.FileOpOptions, root string, walkFn executor.WalkFunc) error {
	ns := v.getNS(opts)
	cleanRoot := filepath.Clean(root)

	pathSet := map[string]struct{}{}

	// Collect from namespace-specific store
	if dirs := v.nsDirs[ns]; dirs != nil {
		if _, ok := dirs[cleanRoot]; ok {
			pathSet[cleanRoot] = struct{}{}
			for dir := range dirs {
				if dir == cleanRoot || strings.HasPrefix(dir+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) {
					pathSet[dir] = struct{}{}
				}
			}
		}
		if files := v.nsFiles[ns]; files != nil {
			for filePath := range files {
				if strings.HasPrefix(filePath+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) {
					pathSet[filePath] = struct{}{}
				}
			}
		}
	}

	// Also from empty namespace
	if ns != "" {
		if dirs := v.nsDirs[""]; dirs != nil {
			if _, ok := dirs[cleanRoot]; ok {
				pathSet[cleanRoot] = struct{}{}
				for dir := range dirs {
					if strings.HasPrefix(dir+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) {
						pathSet[dir] = struct{}{}
					}
				}
			}
			if files := v.nsFiles[""]; files != nil {
				for filePath := range files {
					if strings.HasPrefix(filePath+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) {
						pathSet[filePath] = struct{}{}
					}
				}
			}
		}
	}

	// Also include host filesystem entries via bind mounts
	for _, bind := range opts.ROBinds {
		cleanDest := filepath.Clean(bind.Dest)
		if cleanRoot == cleanDest || strings.HasPrefix(cleanRoot+string(os.PathSeparator), cleanDest+string(os.PathSeparator)) {
			rel := strings.TrimPrefix(cleanRoot, cleanDest)
			hostDir := filepath.Join(bind.Src, rel)
			filepath.Walk(hostDir, func(hostPath string, info os.FileInfo, err error) error {
				if err != nil {
					return nil
				}
				sandboxPath := filepath.Join(cleanRoot, strings.TrimPrefix(hostPath, hostDir))
				pathSet[filepath.Clean(sandboxPath)] = struct{}{}
				return nil
			})
		}
	}

	if len(pathSet) == 0 {
		return os.ErrNotExist
	}

	var paths []string
	for path := range pathSet {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		info, ok := v.infoForPath(opts, path)
		if !ok {
			continue
		}
		if err := walkFn(path, info, nil); err != nil {
			return err
		}
	}
	return nil
}

func (v *virtualFileOperator) CreateFile(_ context.Context, opts executor.FileOpOptions, path string, reader io.Reader) (int64, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	ns := v.getNS(opts)
	files := v.filesForNS(ns)
	dirs := v.dirsForNS(ns)
	cleanPath := filepath.Clean(path)
	files[cleanPath] = string(data)
	for dir := filepath.Dir(cleanPath); ; dir = filepath.Dir(dir) {
		dirs[filepath.Clean(dir)] = struct{}{}
		if dir == "/" || dir == filepath.Dir(dir) {
			break
		}
	}
	return int64(len(data)), nil
}

func (v *virtualFileOperator) MkdirAll(_ context.Context, opts executor.FileOpOptions, path string, _ os.FileMode) error {
	ns := v.getNS(opts)
	dirs := v.dirsForNS(ns)
	dirs[filepath.Clean(path)] = struct{}{}
	return nil
}

func (v *virtualFileOperator) ServeFile(_ context.Context, opts executor.FileOpOptions, path string) (string, func(), error) {
	ns := v.getNS(opts)
	cleanPath := filepath.Clean(path)
	var content string
	var ok bool
	if files := v.nsFiles[ns]; files != nil {
		content, ok = files[cleanPath]
	}
	if !ok && ns != "" {
		content, ok = v.nsFiles[""][cleanPath]
	}
	if !ok {
		return "", nil, os.ErrNotExist
	}
	tmp, err := os.CreateTemp("", "vfs-serve-*")
	if err != nil {
		return "", nil, err
	}
	tmp.WriteString(content)
	tmp.Close()
	return tmp.Name(), func() { os.Remove(tmp.Name()) }, nil
}

func (v *virtualFileOperator) infoForPath(opts executor.FileOpOptions, path string) (executor.FileInfo, bool) {
	ns := v.getNS(opts)
	cleanPath := filepath.Clean(path)

	// Check namespace-specific first
	if info, ok := v.infoForPathInNS(ns, cleanPath); ok {
		return info, true
	}
	if isSharedSandboxPath(opts, cleanPath) {
		for otherNS := range v.nsFiles {
			if otherNS == ns {
				continue
			}
			if info, ok := v.infoForPathInNS(otherNS, cleanPath); ok {
				return info, true
			}
		}
	}
	// Fallback to empty namespace
	if ns != "" {
		if info, ok := v.infoForPathInNS("", cleanPath); ok {
			return info, true
		}
	}
	// Fallback: try host filesystem
	hostPath := hostPathForSandbox(opts, cleanPath)
	if hostPath != "" {
		if info, err := os.Stat(hostPath); err == nil {
			return executor.FileInfo{
				Name:    info.Name(),
				Size:    info.Size(),
				Mode:    info.Mode(),
				ModTime: info.ModTime(),
				IsDir:   info.IsDir(),
			}, true
		}
	}
	return executor.FileInfo{}, false
}

func (v *virtualFileOperator) infoForPathInNS(ns, cleanPath string) (executor.FileInfo, bool) {
	if dirs := v.nsDirs[ns]; dirs != nil {
		if _, ok := dirs[cleanPath]; ok {
			return executor.FileInfo{
				Name:    filepath.Base(cleanPath),
				Mode:    os.ModeDir | 0755,
				ModTime: time.Unix(0, 0),
				IsDir:   true,
			}, true
		}
	}
	if files := v.nsFiles[ns]; files != nil {
		if content, ok := files[cleanPath]; ok {
			return executor.FileInfo{
				Name:    filepath.Base(cleanPath),
				Size:    int64(len(content)),
				Mode:    0644,
				ModTime: time.Unix(0, 0),
				IsDir:   false,
			}, true
		}
	}
	return executor.FileInfo{}, false
}

func TestFileWriteAndRead(t *testing.T) {
	r, _ := setupRouter()

	// Write
	body := `{"agent_id": "a1", "session_id": "test1", "file": "/hello.txt", "content": "hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write failed: %d %s", w.Code, w.Body.String())
	}

	// Read
	body = `{"agent_id": "a1", "session_id": "test1", "file": "/hello.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
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
		t.Errorf("expected 'hello world', got %v", data["content"])
	}
}

func TestFileReadWithLines(t *testing.T) {
	r, _ := setupRouter()

	// Write multi-line file
	body := `{"agent_id": "a1", "session_id": "test2", "file": "/lines.txt", "content": "line1\nline2\nline3\nline4\nline5"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read lines 1-3 (0-based)
	body = `{"agent_id": "a1", "session_id": "test2", "file": "/lines.txt", "start_line": 1, "end_line": 3}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	content := data["content"].(string)
	if content != "line2\nline3" {
		t.Errorf("expected 'line2\\nline3', got %q", content)
	}
}

func TestFileReplace(t *testing.T) {
	r, _ := setupRouter()

	// Write
	body := `{"agent_id": "a1", "session_id": "test3", "file": "/replace.txt", "content": "foo bar foo"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Replace
	body = `{"agent_id": "a1", "session_id": "test3", "file": "/replace.txt", "old_str": "foo", "new_str": "baz"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if int(data["replaced_count"].(float64)) != 2 {
		t.Errorf("expected 2 replacements, got %v", data["replaced_count"])
	}

	// Verify
	body = `{"agent_id": "a1", "session_id": "test3", "file": "/replace.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &resp)
	data = resp["data"].(map[string]interface{})
	if data["content"] != "baz bar baz" {
		t.Errorf("expected 'baz bar baz', got %v", data["content"])
	}
}

func TestFileSearch(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test4", "file": "/search.txt", "content": "hello world\nfoo bar\nhello again"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body = `{"agent_id": "a1", "session_id": "test4", "file": "/search.txt", "regex": "hello"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	matches := data["matches"].([]interface{})
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

func TestFileList(t *testing.T) {
	r, _ := setupRouter()

	// Create files
	for _, f := range []string{"/a.txt", "/b.txt", "/sub/c.txt"} {
		body := fmt.Sprintf(`{"agent_id": "a1", "session_id": "test5", "file": "%s", "content": "data"}`, f)
		req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}

	// List
	body := `{"agent_id": "a1", "session_id": "test5", "path": "/"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) < 3 {
		t.Errorf("expected at least 3 items (a.txt, b.txt, sub/), got %d", len(files))
	}
	// Verify structure: should contain a.txt, b.txt, and sub directory
	names := make(map[string]bool)
	for _, f := range files {
		fi := f.(map[string]interface{})
		names[fi["name"].(string)] = true
	}
	if !names["a.txt"] || !names["b.txt"] || !names["sub"] {
		t.Errorf("expected a.txt, b.txt, sub in listing, got %v", names)
	}
}

func TestFileGlobRecursiveRespectsHiddenFlag(t *testing.T) {
	r, _ := setupRouter()

	for _, f := range []string{"/root.go", "/nested/code.go", "/nested/.hidden.go", "/.hidden-root.go"} {
		body := fmt.Sprintf(`{"agent_id": "a1", "session_id": "glob_recursive", "file": "%s", "content": "package main"}`, f)
		req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("write %s failed: %d %s", f, w.Code, w.Body.String())
		}
	}

	body := `{"agent_id": "a1", "session_id": "glob_recursive", "path": "/", "pattern": "**/*.go"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/glob", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("glob failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) != 2 {
		t.Fatalf("expected 2 visible matches, got %d: %v", len(files), files)
	}

	names := map[string]bool{}
	for _, f := range files {
		fi := f.(map[string]interface{})
		names[fi["path"].(string)] = true
	}
	if !names["/root.go"] || !names["/nested/code.go"] {
		t.Fatalf("unexpected glob paths: %v", names)
	}
}

func TestFileRecursiveAPIs_SkipImplicitSkillsInBwrap(t *testing.T) {
	fileOp := newVirtualFileOperator(map[string]string{
		"/home/README.md":                     "root docs",
		"/home/src/app.txt":                   "needle in workspace",
		"/home/src/main.go":                   "package main",
		"/agents/skills/test-skill/guide.md":  "skill docs",
		"/agents/skills/test-skill/notes.txt": "needle in skills",
	})
	r, _ := setupRouterWithFileOperator(fileOp)

	tests := []struct {
		name   string
		path   string
		body   string
		assert func(*testing.T, map[string]interface{})
	}{
		{
			name: "find",
			path: "/v1/file/find",
			body: `{"agent_id":"a1","session_id":"bwrap_find","path":"/","glob":"*.md"}`,
			assert: func(t *testing.T, data map[string]interface{}) {
				files := data["files"].([]interface{})
				if len(files) != 1 || files[0].(string) != "/README.md" {
					t.Fatalf("expected only /README.md, got %v", files)
				}
			},
		},
		{
			name: "grep",
			path: "/v1/file/grep",
			body: `{"agent_id":"a1","session_id":"bwrap_grep","path":"/","pattern":"needle"}`,
			assert: func(t *testing.T, data map[string]interface{}) {
				matches := data["matches"].([]interface{})
				if len(matches) != 1 {
					t.Fatalf("expected 1 grep match, got %v", matches)
				}
				match := matches[0].(map[string]interface{})
				if match["file"] != "/src/app.txt" {
					t.Fatalf("expected workspace grep match, got %v", match)
				}
			},
		},
		{
			name: "glob",
			path: "/v1/file/glob",
			body: `{"agent_id":"a1","session_id":"bwrap_glob","path":"/","pattern":"**/*.md"}`,
			assert: func(t *testing.T, data map[string]interface{}) {
				files := data["files"].([]interface{})
				if len(files) != 1 {
					t.Fatalf("expected 1 glob result, got %v", files)
				}
				file := files[0].(map[string]interface{})
				if file["path"] != "/README.md" {
					t.Fatalf("expected only /README.md, got %v", file)
				}
			},
		},
		{
			name: "recursive list",
			path: "/v1/file/list",
			body: `{"agent_id":"a1","session_id":"bwrap_list","path":"/","recursive":true}`,
			assert: func(t *testing.T, data map[string]interface{}) {
				files := data["files"].([]interface{})
				for _, entry := range files {
					file := entry.(map[string]interface{})
					if strings.HasPrefix(file["path"].(string), SandboxSkillsDir) {
						t.Fatalf("expected recursive list to skip implicit skills tree, got %v", files)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("%s failed: %d %s", tt.name, w.Code, w.Body.String())
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v", err)
			}
			data := resp["data"].(map[string]interface{})
			tt.assert(t, data)
		})
	}
}

func TestFileGlob_AllowsExplicitSkillsSearchInBwrap(t *testing.T) {
	fileOp := newVirtualFileOperator(map[string]string{
		"/home/project/README.md":             "workspace docs",
		"/agents/skills/test-skill/guide.md":  "skill docs",
		"/agents/skills/test-skill/notes.txt": "skill notes",
	})
	r, _ := setupRouterWithFileOperator(fileOp)

	body := `{"agent_id":"a1","session_id":"bwrap_skills","path":"/agents/skills","pattern":"**/*.md"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/glob", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("explicit skills glob failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) != 1 {
		t.Fatalf("expected 1 explicit skills glob result, got %v", files)
	}
	file := files[0].(map[string]interface{})
	if file["path"] != "/agents/skills/test-skill/guide.md" {
		t.Fatalf("expected /agents/skills/test-skill/guide.md, got %v", file)
	}
}

func TestFileRecursiveAPIs_SkipImplicitProjectdataButAllowExplicitInBwrap(t *testing.T) {
	fileOp := newVirtualFileOperator(map[string]string{
		"/home/app.txt":                   "workspace needle",
		"/home/project/docs/guide.md":     "project docs needle",
		"/home/project/docs/internal.txt": "project internal needle",
	})
	r, _ := setupRouterWithFileOperator(fileOp)

	t.Run("implicit root grep skips projectdata", func(t *testing.T) {
		body := `{"agent_id":"a1","session_id":"bwrap_project_implicit","path":"/","pattern":"needle","project_id":"proj-a"}`
		req := httptest.NewRequest(http.MethodPost, "/v1/file/grep", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("implicit grep failed: %d %s", w.Code, w.Body.String())
		}
		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		matches := resp["data"].(map[string]interface{})["matches"].([]interface{})
		if len(matches) != 1 || matches[0].(map[string]interface{})["file"] != "/app.txt" {
			t.Fatalf("expected only workspace match, got %v", matches)
		}
	})

	tests := []struct {
		name string
		path string
		body string
		want string
	}{
		{name: "find", path: "/v1/file/find", body: `{"agent_id":"a1","session_id":"bwrap_project_find","path":"/home/project","glob":"*.md","project_id":"proj-a"}`, want: "/home/project/docs/guide.md"},
		{name: "grep", path: "/v1/file/grep", body: `{"agent_id":"a1","session_id":"bwrap_project_grep","path":"/home/project","pattern":"project docs","project_id":"proj-a"}`, want: "/home/project/docs/guide.md"},
		{name: "glob", path: "/v1/file/glob", body: `{"agent_id":"a1","session_id":"bwrap_project_glob","path":"/home/project","pattern":"**/*.md","project_id":"proj-a"}`, want: "/home/project/docs/guide.md"},
		{name: "list", path: "/v1/file/list", body: `{"agent_id":"a1","session_id":"bwrap_project_list","path":"/home/project","recursive":true,"project_id":"proj-a"}`, want: "/home/project/docs"},
	}

	for _, tt := range tests {
		t.Run("explicit "+tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s failed: %d %s", tt.name, w.Code, w.Body.String())
			}

			var resp map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &resp)
			data := resp["data"].(map[string]interface{})
			switch tt.name {
			case "find":
				files := data["files"].([]interface{})
				if len(files) != 1 || files[0] != tt.want {
					t.Fatalf("expected %s, got %v", tt.want, files)
				}
			case "grep":
				matches := data["matches"].([]interface{})
				if len(matches) != 1 || matches[0].(map[string]interface{})["file"] != tt.want {
					t.Fatalf("expected %s, got %v", tt.want, matches)
				}
			case "glob":
				files := data["files"].([]interface{})
				if len(files) != 1 || files[0].(map[string]interface{})["path"] != tt.want {
					t.Fatalf("expected %s, got %v", tt.want, files)
				}
			case "list":
				files := data["files"].([]interface{})
				if len(files) == 0 || files[0].(map[string]interface{})["path"] != tt.want {
					t.Fatalf("expected first path %s, got %v", tt.want, files)
				}
			}
		})
	}
}

func TestFileProjectdata_DirectModeSharedAndDisplayPaths(t *testing.T) {
	r, _ := setupRouter()

	writeBody := `{"agent_id":"a1","session_id":"project_s1","file":"/home/project/docs/readme.md","content":"shared docs","project_id":"proj-a"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(writeBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("write projectdata failed: %d %s", w.Code, w.Body.String())
	}

	readBody := `{"agent_id":"a2","session_id":"project_s2","file":"/home/project/docs/readme.md","project_id":"proj-a"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(readBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("read shared projectdata failed: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["data"].(map[string]interface{})["content"] != "shared docs" {
		t.Fatalf("unexpected read content: %v", resp)
	}

	listBody := `{"agent_id":"a1","session_id":"project_s1","path":"/home/project","recursive":true,"project_id":"proj-a"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/list", bytes.NewBufferString(listBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list projectdata failed: %d %s", w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	files := resp["data"].(map[string]interface{})["files"].([]interface{})
	paths := map[string]bool{}
	for _, entry := range files {
		paths[entry.(map[string]interface{})["path"].(string)] = true
	}
	if !paths["/home/project/docs"] || !paths["/home/project/docs/readme.md"] {
		t.Fatalf("unexpected projectdata display paths: %v", paths)
	}
}

func TestFileFind(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test6", "file": "/readme.md", "content": "# Hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body = `{"agent_id": "a1", "session_id": "test6", "path": "/", "glob": "*.md"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/find", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(files), files)
	}
}

func TestFileGrep(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test7", "file": "/grep_test.txt", "content": "hello world\nfoo bar\nhello again\nbaz"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	body = `{"agent_id": "a1", "session_id": "test7", "path": "/", "pattern": "hello", "include": ["grep_test.txt"]}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/grep", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	matches := data["matches"].([]interface{})
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d", len(matches))
	}
}

func TestFileWriteAutoMkdir(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test8", "file": "/deep/nested/dir/file.txt", "content": "auto created"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write with auto mkdir failed: %d %s", w.Code, w.Body.String())
	}
}

func TestFileAppend(t *testing.T) {
	r, _ := setupRouter()

	// Initial write
	body := `{"agent_id": "a1", "session_id": "test9", "file": "/append.txt", "content": "line1"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Append
	body = `{"agent_id": "a1", "session_id": "test9", "file": "/append.txt", "content": "line2", "append": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read
	body = `{"agent_id": "a1", "session_id": "test9", "file": "/append.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "line1line2" {
		t.Errorf("expected 'line1line2', got %v", data["content"])
	}
}

func TestFileUpload(t *testing.T) {
	r, _ := setupRouter()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("agent_id", "a1")
	writer.WriteField("session_id", "test10")
	writer.WriteField("path", "/uploaded.txt")
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded content"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/file/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload failed: %d %s", w.Code, w.Body.String())
	}

	// Verify the uploaded content by reading it back
	readBody := `{"agent_id": "a1", "session_id": "test10", "file": "/uploaded.txt"}`
	readReq := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(readBody))
	readReq.Header.Set("Content-Type", "application/json")
	readW := httptest.NewRecorder()
	r.ServeHTTP(readW, readReq)

	if readW.Code != http.StatusOK {
		t.Fatalf("read after upload failed: %d %s", readW.Code, readW.Body.String())
	}
	var readResp map[string]interface{}
	json.Unmarshal(readW.Body.Bytes(), &readResp)
	readData := readResp["data"].(map[string]interface{})
	if readData["content"] != "uploaded content" {
		t.Errorf("expected uploaded content 'uploaded content', got %v", readData["content"])
	}
}

func TestFileDownload(t *testing.T) {
	r, _ := setupRouter()

	// Write first
	body := `{"agent_id": "a1", "session_id": "test11", "file": "/download.txt", "content": "download me"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Download
	req = httptest.NewRequest(http.MethodGet, "/v1/file/download?agent_id=a1&session_id=test11&path=/download.txt", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download failed: %d %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "download me" {
		t.Errorf("expected 'download me', got %s", w.Body.String())
	}
}

func TestSessionIsolation(t *testing.T) {
	r, _ := setupRouter()

	// Write to session A
	body := `{"agent_id": "a1", "session_id": "sessA", "file": "/secret.txt", "content": "secret A"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write to session B
	body = `{"agent_id": "a1", "session_id": "sessB", "file": "/secret.txt", "content": "secret B"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read session A
	body = `{"agent_id": "a1", "session_id": "sessA", "file": "/secret.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "secret A" {
		t.Errorf("session isolation broken: expected 'secret A', got %v", data["content"])
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test12", "file": "/../../../etc/passwd", "content": "hack"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("path traversal should be blocked, got %d", w.Code)
	}
}

func TestFileWriteBase64(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test13", "file": "/binary.bin", "content": "SGVsbG8gV29ybGQ=", "encoding": "base64"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("base64 write failed: %d %s", w.Code, w.Body.String())
	}

	// Read back
	body = `{"agent_id": "a1", "session_id": "test13", "file": "/binary.bin"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "Hello World" {
		t.Errorf("expected 'Hello World', got %v", data["content"])
	}
}

func TestSkillsPathReadOnly(t *testing.T) {
	r, mgr := setupRouter()

	// Create a file in skills directory
	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "test-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "test-skill", "SKILLS.MD"), []byte("---\nname: test\n---\ncontent"), 0644)

	// Write to skills path should fail
	body := `{"agent_id": "a1", "session_id": "test14", "file": "/agents/skills/test-skill/new.txt", "content": "hack"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("write to skills should be forbidden, got %d", w.Code)
	}

	// Replace in skills path should fail
	body = `{"agent_id": "a1", "session_id": "test14", "file": "/agents/skills/test-skill/SKILLS.MD", "old_str": "content", "new_str": "hacked"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("replace in skills should be forbidden, got %d", w.Code)
	}

	// Read from skills path should work
	body = `{"agent_id": "a1", "session_id": "test14", "file": "/agents/skills/test-skill/SKILLS.MD"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("read from skills should succeed, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSkillsPathList(t *testing.T) {
	r, mgr := setupRouter()

	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "my-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "my-skill", "SKILLS.MD"), []byte("---\nname: my-skill\n---\ncontent"), 0644)

	body := `{"agent_id": "a1", "session_id": "test15", "path": "/agents/skills"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/list", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list skills failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	files := data["files"].([]interface{})
	if len(files) < 1 {
		t.Errorf("expected at least 1 item in skills listing, got %d", len(files))
	}
}

func TestAgentIsolation(t *testing.T) {
	r, _ := setupRouter()

	// Write to agent a1
	body := `{"agent_id": "a1", "session_id": "sess1", "file": "/secret.txt", "content": "agent1 secret"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Write to agent a2
	body = `{"agent_id": "a2", "session_id": "sess1", "file": "/secret.txt", "content": "agent2 secret"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Read agent a1
	body = `{"agent_id": "a1", "session_id": "sess1", "file": "/secret.txt"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "agent1 secret" {
		t.Errorf("agent isolation broken: expected 'agent1 secret', got %v", data["content"])
	}
}

func TestFileReadNotFound(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test20", "file": "/nonexistent.txt"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for nonexistent file, got %d", w.Code)
	}
}

func TestFileSearchInvalidRegex(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test21", "file": "/test.txt", "regex": "[invalid"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid regex, got %d", w.Code)
	}
}

func TestFileReplaceNoMatch(t *testing.T) {
	r, _ := setupRouter()

	// Write a file
	body := `{"agent_id": "a1", "session_id": "test22", "file": "/nomatch.txt", "content": "hello world"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Replace with non-matching old_str
	body = `{"agent_id": "a1", "session_id": "test22", "file": "/nomatch.txt", "old_str": "xyz", "new_str": "abc"}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("replace no match: expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if int(data["replaced_count"].(float64)) != 0 {
		t.Errorf("expected 0 replacements, got %v", data["replaced_count"])
	}
}

func TestPathTraversalReadBlocked(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "test23", "file": "/../../../etc/passwd"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("path traversal read should be blocked, got %d", w.Code)
	}
}

func TestFileWriteMissingRequired(t *testing.T) {
	r, _ := setupRouter()

	// Missing file field
	body := `{"agent_id": "a1", "session_id": "test24", "content": "data"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing file field, got %d", w.Code)
	}
}

func TestFileWrite_AgentWorkspace(t *testing.T) {
	r, _ := setupRouter()

	// Write with enable_agent_workspace=true
	body := `{"agent_id": "a1", "session_id": "test_dsi", "file": "/workspace-file.txt", "content": "in workspace", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	// Verify via API read back
	body = `{"agent_id": "a1", "session_id": "test_dsi", "file": "/workspace-file.txt", "enable_agent_workspace": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("read back workspace file failed: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "in workspace" {
		t.Errorf("expected 'in workspace', got %v", data["content"])
	}

	// With virtualFileOperator, workspace and session both resolve to /home,
	// so the file is still readable without enable_agent_workspace.
	// In real bwrap, different bind mounts would isolate these.
	if w.Code != http.StatusOK {
		t.Errorf("expected file to be readable (virtual FS shares /home), got %d", w.Code)
	}
}

func TestFileRead_AgentWorkspace(t *testing.T) {
	// Use a virtual FS with the workspace file pre-created
	vfs := newWritableVirtualFileOperator(nil)
	vfs.nsFiles[""]["/home/ws-read-test.txt"] = "workspace content"
	r, _ := setupRouterWithFileOperator(vfs)

	// Read with enable_agent_workspace=true
	body := `{"agent_id": "a1", "session_id": "test_dsi_read", "file": "/ws-read-test.txt", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("read with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "workspace content" {
		t.Errorf("expected 'workspace content', got %v", data["content"])
	}
}

func TestFileWrite_AgentWorkspace_Skills(t *testing.T) {
	r, _ := setupRouter()

	// Pre-create the skills directory with a skill
	skillsDir := filepath.Join(os.TempDir(), "sandbox-file-test-"+t.Name(), "agents", "a1", "skills")
	os.MkdirAll(filepath.Join(skillsDir, "my-skill"), 0755)

	// Write to skills path with enable_agent_workspace=true — skills are always RO
	body := `{"agent_id": "a1", "session_id": "test_sw", "file": "/agents/skills/my-skill/new-file.txt", "content": "skill data", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for skills write even with agent_workspace, got %d %s", w.Code, w.Body.String())
	}
}

func TestFileReplace_AgentWorkspace_Skills(t *testing.T) {
	r, _ := setupRouter()

	// Pre-create a skill file in the skills directory
	skillsDir := filepath.Join(os.TempDir(), "sandbox-file-test-"+t.Name(), "agents", "a1", "skills")
	os.MkdirAll(filepath.Join(skillsDir, "replace-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "replace-skill", "config.txt"), []byte("foo bar foo"), 0644)

	// Replace in skills path with enable_agent_workspace=true — skills are always RO
	body := `{"agent_id": "a1", "session_id": "test_sw_replace", "file": "/agents/skills/replace-skill/config.txt", "old_str": "foo", "new_str": "baz", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/replace", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for skills replace even with agent_workspace, got %d %s", w.Code, w.Body.String())
	}
}

func TestFileUpload_AgentWorkspace(t *testing.T) {
	r, _ := setupRouter()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("agent_id", "a1")
	writer.WriteField("session_id", "test_dsi_upload")
	writer.WriteField("path", "/upload-ws.txt")
	writer.WriteField("enable_agent_workspace", "true")
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded in workspace mode"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/file/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("upload with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	// Verify via API read back
	readBody := `{"agent_id": "a1", "session_id": "test_dsi_upload", "file": "/upload-ws.txt", "enable_agent_workspace": true}`
	readReq := httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(readBody))
	readReq.Header.Set("Content-Type", "application/json")
	readW := httptest.NewRecorder()
	r.ServeHTTP(readW, readReq)

	if readW.Code != http.StatusOK {
		t.Fatalf("read back workspace upload failed: %d %s", readW.Code, readW.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(readW.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "uploaded in workspace mode" {
		t.Errorf("expected 'uploaded in workspace mode', got %v", data["content"])
	}
}

func TestFileUpload_AgentWorkspace_Skills(t *testing.T) {
	r, _ := setupRouter()

	// Pre-create skills directory
	skillsDir := filepath.Join(os.TempDir(), "sandbox-file-test-"+t.Name(), "agents", "a1", "skills")
	os.MkdirAll(filepath.Join(skillsDir, "upload-skill"), 0755)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	writer.WriteField("agent_id", "a1")
	writer.WriteField("session_id", "test_sw_upload")
	writer.WriteField("path", "/agents/skills/upload-skill/uploaded.txt")
	writer.WriteField("enable_agent_workspace", "true")
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("uploaded to skills"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/file/upload", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Skills are always read-only regardless of agent_workspace
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for skills upload even with agent_workspace, got %d %s", w.Code, w.Body.String())
	}
}

// TestFileWrite_AgentWorkspace_SkillsAndWorkspace verifies that enable_agent_workspace=true
// enables workspace-mode path resolution, but skills remain read-only.
func TestFileWrite_AgentWorkspace_SkillsAndWorkspace(t *testing.T) {
	r, _ := setupRouter()

	// Write to skills path with enable_agent_workspace — skills are always RO
	body := `{"agent_id": "a1", "session_id": "test_both", "file": "/agents/skills/both-skill/combined.txt", "content": "both flags", "enable_agent_workspace": true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for skills write even with agent_workspace, got %d %s", w.Code, w.Body.String())
	}

	// Write to a non-skills path — should resolve to workspace dir, not session dir
	body = `{"agent_id": "a1", "session_id": "test_both", "file": "/workspace-both.txt", "content": "ws with both", "enable_agent_workspace": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("write workspace file with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}

	// Verify via API read back
	body = `{"agent_id": "a1", "session_id": "test_both", "file": "/workspace-both.txt", "enable_agent_workspace": true}`
	req = httptest.NewRequest(http.MethodPost, "/v1/file/read", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("read back workspace file failed: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data := resp["data"].(map[string]interface{})
	if data["content"] != "ws with both" {
		t.Errorf("expected 'ws with both', got %v", data["content"])
	}
}

// TestFileDownload_AgentWorkspace verifies download from workspace dir with enable_agent_workspace.
func TestFileDownload_AgentWorkspace(t *testing.T) {
	// Pre-create a file in the virtual FS
	vfs := newWritableVirtualFileOperator(nil)
	vfs.nsFiles[""]["/home/dl-test.txt"] = "download me"
	r, _ := setupRouterWithFileOperator(vfs)

	req := httptest.NewRequest(http.MethodGet,
		"/v1/file/download?agent_id=a1&session_id=dl_ws&path=/dl-test.txt&enable_agent_workspace=true", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download with enable_agent_workspace failed: %d %s", w.Code, w.Body.String())
	}
	if w.Body.String() != "download me" {
		t.Errorf("expected 'download me', got %q", w.Body.String())
	}
}

// TestFileWrite_SkillsReadOnly_Default verifies skills are read-only when enable_agent_workspace is false.
func TestFileWrite_SkillsReadOnly_Default(t *testing.T) {
	r, mgr := setupRouter()

	skillsDir := mgr.SkillsRoot("a1")
	os.MkdirAll(filepath.Join(skillsDir, "ro-skill"), 0755)

	// Write to skills path WITHOUT enable_agent_workspace — should be blocked
	body := `{"agent_id": "a1", "session_id": "test_ro", "file": "/agents/skills/ro-skill/blocked.txt", "content": "nope"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 for skills write without enable_agent_workspace, got %d", w.Code)
	}
}

func TestFileWrite_LegacySkillsPathIsOrdinaryDirectory(t *testing.T) {
	r, _ := setupRouter()

	body := `{"agent_id": "a1", "session_id": "alias_ro", "file": "/skills/aliased-skill/blocked.txt", "content": "nope"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/file/write", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected /skills path write to behave like an ordinary directory, got %d %s", w.Code, w.Body.String())
	}
}

func TestFileOpOpts_SkillsReadOnlyOutsideWorkspace(t *testing.T) {
	_, mgr := setupRouter()
	udMgr := userdata.NewManager("/tmp/test-users")
	pdMgr := projectdata.NewManager("/tmp/test-projects")
	h := NewFileHandler(mgr, udMgr, pdMgr, newWritableVirtualFileOperator(nil))

	sessionOpts, err := h.fileOpOpts("a1", "s1", "", "", false)
	if err != nil {
		t.Fatalf("fileOpOpts: %v", err)
	}
	if len(sessionOpts.RWBinds) != 1 || sessionOpts.RWBinds[0].Src != mgr.SessionRoot("a1", "s1") {
		t.Fatalf("session RWBinds = %v", sessionOpts.RWBinds)
	}
	if sessionOpts.RWBinds[0].Dest != SandboxRoot {
		t.Fatalf("session RWBind Dest = %v, want %s", sessionOpts.RWBinds[0].Dest, SandboxRoot)
	}
	if len(sessionOpts.ROBinds) != 1 || sessionOpts.ROBinds[0].Src != mgr.SkillsRoot("a1") {
		t.Fatalf("session ROBinds = %v", sessionOpts.ROBinds)
	}
	if sessionOpts.ROBinds[0].Dest != SandboxSkillsDir {
		t.Fatalf("session ROBind Dest = %v, want %s", sessionOpts.ROBinds[0].Dest, SandboxSkillsDir)
	}

	workspaceOpts, err := h.fileOpOpts("a1", "s1", "", "", true)
	if err != nil {
		t.Fatalf("fileOpOpts: %v", err)
	}
	if len(workspaceOpts.RWBinds) != 1 || workspaceOpts.RWBinds[0].Src != mgr.WorkspaceRoot("a1") {
		t.Fatalf("workspace RWBinds = %v", workspaceOpts.RWBinds)
	}
	if workspaceOpts.RWBinds[0].Dest != SandboxRoot {
		t.Fatalf("workspace RWBind Dest = %v, want %s", workspaceOpts.RWBinds[0].Dest, SandboxRoot)
	}
	// Skills are always read-only, even in workspace mode
	if len(workspaceOpts.ROBinds) != 1 || workspaceOpts.ROBinds[0].Src != mgr.SkillsRoot("a1") {
		t.Fatalf("workspace ROBinds = %v", workspaceOpts.ROBinds)
	}
	if workspaceOpts.ROBinds[0].Dest != SandboxSkillsDir {
		t.Fatalf("workspace ROBind Dest = %v, want %s", workspaceOpts.ROBinds[0].Dest, SandboxSkillsDir)
	}
}

// ---- resolveSandboxPath table-driven tests ----

func TestResolveSandboxPath(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		// Default: bare paths map to /home
		{"/hello.txt", "/home/hello.txt", false},
		{"/dir/sub/file.txt", "/home/dir/sub/file.txt", false},
		{"/", "/home", false},

		// /home passes through
		{"/home/existing", "/home/existing", false},
		{"/home", "/home", false},

		// /agents/skills passes through
		{"/agents/skills", "/agents/skills", false},
		{"/agents/skills/my-skill/run.sh", "/agents/skills/my-skill/run.sh", false},

		// Relative paths get prefixed with /
		{"hello.txt", "/home/hello.txt", false},

		// Non-canonical names behave like ordinary directories under /home.
		{"/userdata/file.txt", "/home/userdata/file.txt", false},
		{"/projectdata/file.txt", "/home/projectdata/file.txt", false},
		{"/skills/foo", "/home/skills/foo", false},

		// Path traversal blocked
		{"/../../../etc/passwd", "", true},
		{"/../escape", "", true},
		{"/foo/../../bar", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := resolveSandboxPath(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveSandboxPath(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("resolveSandboxPath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// ---- validatePathRequirements tests ----

func TestValidatePathRequirements(t *testing.T) {
	tests := []struct {
		name       string
		reqPath    string
		wantErr    bool
		errContain string
	}{
		{"normal path ok", "/hello.txt", false, ""},
		{"skills path ok without ids", "/agents/skills/foo", false, ""},
		{"userdata is ordinary dir", "/userdata/file.txt", false, ""},
		{"projectdata is ordinary dir", "/projectdata/file.txt", false, ""},
		{"skills is ordinary dir", "/skills/foo", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePathRequirements(tt.reqPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePathRequirements() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errContain != "" && !strings.Contains(err.Error(), tt.errContain) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errContain)
			}
		})
	}
}

// ---- validateWritableSandboxPath tests ----

func TestValidateWritableSandboxPath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"home file writable", "/home/file.txt", nil},
		{"home writable", "/home", nil},
		{"deep path writable", "/home/deep/nested/dir/file.txt", nil},
		{"skills dir readonly", "/agents/skills", errSkillsReadOnly},
		{"skills file readonly", "/agents/skills/my-skill/run.sh", errSkillsReadOnly},
		{"outside home", "/tmp/file.txt", errPathOutsideWriteRoot},
		{"user home writable", "/home/file.txt", nil},
		{"projectdata writable", "/home/project/file.txt", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWritableSandboxPath(tt.path)
			if tt.wantErr == nil && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("expected %v, got %v", tt.wantErr, err)
			}
		})
	}
}
