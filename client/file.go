package client

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

// FileRead reads a file's content.
func (c *Client) FileRead(agentID, sessionID, file string, opts ...FileReadOption) (*FileReadResult, error) {
	req := fileReadRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		File:      file,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileReadResult
	if err := c.post("/v1/file/read", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileWrite writes content to a file.
func (c *Client) FileWrite(agentID, sessionID, file, content string, opts ...FileWriteOption) (*FileWriteResult, error) {
	req := fileWriteRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		File:      file,
		Content:   content,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileWriteResult
	if err := c.post("/v1/file/write", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileReplace replaces all occurrences of oldStr with newStr in a file.
func (c *Client) FileReplace(agentID, sessionID, file, oldStr, newStr string, opts ...FileReplaceOption) (*FileReplaceResult, error) {
	req := fileReplaceRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		File:      file,
		OldStr:    oldStr,
		NewStr:    newStr,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileReplaceResult
	if err := c.post("/v1/file/replace", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileSearch searches within a file using a regex pattern.
func (c *Client) FileSearch(agentID, sessionID, file, regex string, opts ...FileSearchOption) (*FileSearchResult, error) {
	req := fileSearchRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		File:      file,
		Regex:     regex,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileSearchResult
	if err := c.post("/v1/file/search", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileFind finds files matching a glob pattern.
func (c *Client) FileFind(agentID, sessionID, path, glob string, opts ...FileFindOption) (*FileFindResult, error) {
	req := fileFindRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Path:      path,
		Glob:      glob,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileFindResult
	if err := c.post("/v1/file/find", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileGrep searches across files for a pattern.
func (c *Client) FileGrep(agentID, sessionID, path, pattern string, opts ...FileGrepOption) (*FileGrepResult, error) {
	req := fileGrepRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Path:      path,
		Pattern:   pattern,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileGrepResult
	if err := c.post("/v1/file/grep", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileGlob returns files matching a glob pattern.
func (c *Client) FileGlob(agentID, sessionID, path, pattern string, opts ...FileGlobOption) (*FileGlobResult, error) {
	req := fileGlobRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Path:      path,
		Pattern:   pattern,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileGlobResult
	if err := c.post("/v1/file/glob", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileList lists directory contents.
func (c *Client) FileList(agentID, sessionID, path string, opts ...FileListOption) (*FileListResult, error) {
	req := fileListRequest{
		AgentID:   agentID,
		SessionID: sessionID,
		Path:      path,
	}
	for _, o := range opts {
		o(&req)
	}

	var result FileListResult
	if err := c.post("/v1/file/list", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileUpload uploads a file via multipart form.
func (c *Client) FileUpload(agentID, sessionID, path string, reader io.Reader, filename string, opts ...FileUploadOption) (*FileUploadResult, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	w.WriteField("agent_id", agentID)
	w.WriteField("session_id", sessionID)
	w.WriteField("path", path)

	// Apply options via uploadRequest
	ur := &fileUploadOptions{}
	for _, o := range opts {
		o(ur)
	}
	if ur.DisableSessionIsolation {
		w.WriteField("disable_session_isolation", "true")
	}
	if ur.SkillsWritable {
		w.WriteField("skills_writable", "true")
	}

	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, reader); err != nil {
		return nil, fmt.Errorf("copy file content: %w", err)
	}
	w.Close()

	httpReq, err := http.NewRequest(http.MethodPost, c.baseURL+"/v1/file/upload", &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	var result FileUploadResult
	if err := c.handleResponse(resp, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// FileDownload downloads a file and returns its content.
// For large files, consider using a custom http.Client with appropriate timeout.
func (c *Client) FileDownload(agentID, sessionID, path string, opts ...FileDownloadOption) ([]byte, error) {
	params := url.Values{
		"agent_id":   {agentID},
		"session_id": {sessionID},
		"path":       {path},
	}

	fd := &fileDownloadOptions{}
	for _, o := range opts {
		o(fd)
	}
	if fd.DisableSessionIsolation {
		params.Set("disable_session_isolation", "true")
	}

	httpReq, err := http.NewRequest(http.MethodGet, c.baseURL+"/v1/file/download?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("download file: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, &Error{StatusCode: resp.StatusCode, Message: "download failed"}
	}

	return io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
}

// --- Internal request types ---

type fileReadRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	File      string `json:"file"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
	DisableSessionIsolation bool   `json:"disable_session_isolation"`
}

type fileWriteRequest struct {
	AgentID         string `json:"agent_id"`
	SessionID       string `json:"session_id"`
	File            string `json:"file"`
	Content         string `json:"content"`
	Encoding        string `json:"encoding,omitempty"`
	Append          bool   `json:"append"`
	LeadingNewline  bool   `json:"leading_newline"`
	TrailingNewline bool   `json:"trailing_newline"`
	DisableSessionIsolation bool   `json:"disable_session_isolation"`
	SkillsWritable          bool   `json:"skills_writable"`
}

type fileReplaceRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	File      string `json:"file"`
	OldStr    string `json:"old_str"`
	NewStr    string `json:"new_str"`
	DisableSessionIsolation bool   `json:"disable_session_isolation"`
	SkillsWritable          bool   `json:"skills_writable"`
}

type fileSearchRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	File      string `json:"file"`
	Regex     string `json:"regex"`
	DisableSessionIsolation bool   `json:"disable_session_isolation"`
}

type fileFindRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Glob      string `json:"glob"`
	DisableSessionIsolation bool   `json:"disable_session_isolation"`
}

type fileGrepRequest struct {
	AgentID         string   `json:"agent_id"`
	SessionID       string   `json:"session_id"`
	Path            string   `json:"path"`
	Pattern         string   `json:"pattern"`
	Include         []string `json:"include,omitempty"`
	Exclude         []string `json:"exclude,omitempty"`
	CaseInsensitive bool     `json:"case_insensitive"`
	FixedStrings    bool     `json:"fixed_strings"`
	ContextBefore   int      `json:"context_before"`
	ContextAfter    int      `json:"context_after"`
	MaxResults      int      `json:"max_results"`
	Recursive       *bool    `json:"recursive,omitempty"`
	DisableSessionIsolation bool     `json:"disable_session_isolation"`
}

type fileGlobRequest struct {
	AgentID         string   `json:"agent_id"`
	SessionID       string   `json:"session_id"`
	Path            string   `json:"path"`
	Pattern         string   `json:"pattern"`
	Exclude         []string `json:"exclude,omitempty"`
	IncludeHidden   bool     `json:"include_hidden"`
	FilesOnly       *bool    `json:"files_only,omitempty"`
	IncludeMetadata *bool    `json:"include_metadata,omitempty"`
	MaxResults      int      `json:"max_results"`
	DisableSessionIsolation bool     `json:"disable_session_isolation"`
}

type fileListRequest struct {
	AgentID            string   `json:"agent_id"`
	SessionID          string   `json:"session_id"`
	Path               string   `json:"path"`
	Recursive          bool     `json:"recursive"`
	ShowHidden         *bool    `json:"show_hidden,omitempty"`
	FileTypes          []string `json:"file_types,omitempty"`
	MaxDepth           *int     `json:"max_depth,omitempty"`
	IncludeSize        *bool    `json:"include_size,omitempty"`
	IncludePermissions *bool    `json:"include_permissions,omitempty"`
	DisableSessionIsolation bool     `json:"disable_session_isolation"`
}

// --- Functional options ---

// FileReadOption is a functional option for FileRead.
type FileReadOption func(*fileReadRequest)

// WithLineRange sets the line range for reading (0-based, exclusive end).
func WithLineRange(start, end int) FileReadOption {
	return func(r *fileReadRequest) { r.StartLine = &start; r.EndLine = &end }
}

// FileWriteOption is a functional option for FileWrite.
type FileWriteOption func(*fileWriteRequest)

// WithAppend enables append mode.
func WithAppend(append bool) FileWriteOption {
	return func(r *fileWriteRequest) { r.Append = append }
}

// WithEncoding sets the content encoding (e.g. "base64").
func WithEncoding(encoding string) FileWriteOption {
	return func(r *fileWriteRequest) { r.Encoding = encoding }
}

// WithLeadingNewline prepends a newline before the content.
func WithLeadingNewline(v bool) FileWriteOption {
	return func(r *fileWriteRequest) { r.LeadingNewline = v }
}

// WithTrailingNewline appends a newline after the content.
func WithTrailingNewline(v bool) FileWriteOption {
	return func(r *fileWriteRequest) { r.TrailingNewline = v }
}

// FileGrepOption is a functional option for FileGrep.
type FileGrepOption func(*fileGrepRequest)

// WithInclude sets file include patterns for grep.
func WithInclude(patterns []string) FileGrepOption {
	return func(r *fileGrepRequest) { r.Include = patterns }
}

// WithCaseInsensitive enables case-insensitive grep.
func WithCaseInsensitive(ci bool) FileGrepOption {
	return func(r *fileGrepRequest) { r.CaseInsensitive = ci }
}

// FileGlobOption is a functional option for FileGlob.
type FileGlobOption func(*fileGlobRequest)

// WithGlobIncludeHidden includes hidden files in glob results.
func WithGlobIncludeHidden(hidden bool) FileGlobOption {
	return func(r *fileGlobRequest) { r.IncludeHidden = hidden }
}

// FileListOption is a functional option for FileList.
type FileListOption func(*fileListRequest)

// WithRecursive enables recursive listing.
func WithRecursive(recursive bool) FileListOption {
	return func(r *fileListRequest) { r.Recursive = recursive }
}

// --- Session Isolation & Skills Writable Options ---

// WithFileReadDisableSessionIsolation disables session isolation for FileRead.
func WithFileReadDisableSessionIsolation() FileReadOption {
	return func(r *fileReadRequest) { r.DisableSessionIsolation = true }
}

// WithFileWriteDisableSessionIsolation disables session isolation for FileWrite.
func WithFileWriteDisableSessionIsolation() FileWriteOption {
	return func(r *fileWriteRequest) { r.DisableSessionIsolation = true }
}

// WithSkillsWritable enables writing to the skills directory.
func WithSkillsWritable() FileWriteOption {
	return func(r *fileWriteRequest) { r.SkillsWritable = true }
}

// FileReplaceOption is a functional option for FileReplace.
type FileReplaceOption func(*fileReplaceRequest)

// WithFileReplaceDisableSessionIsolation disables session isolation for FileReplace.
func WithFileReplaceDisableSessionIsolation() FileReplaceOption {
	return func(r *fileReplaceRequest) { r.DisableSessionIsolation = true }
}

// WithReplaceSkillsWritable enables writing to the skills directory for FileReplace.
func WithReplaceSkillsWritable() FileReplaceOption {
	return func(r *fileReplaceRequest) { r.SkillsWritable = true }
}

// FileSearchOption is a functional option for FileSearch.
type FileSearchOption func(*fileSearchRequest)

// WithFileSearchDisableSessionIsolation disables session isolation for FileSearch.
func WithFileSearchDisableSessionIsolation() FileSearchOption {
	return func(r *fileSearchRequest) { r.DisableSessionIsolation = true }
}

// FileFindOption is a functional option for FileFind.
type FileFindOption func(*fileFindRequest)

// WithFileFindDisableSessionIsolation disables session isolation for FileFind.
func WithFileFindDisableSessionIsolation() FileFindOption {
	return func(r *fileFindRequest) { r.DisableSessionIsolation = true }
}

// WithGrepDisableSessionIsolation disables session isolation for FileGrep.
func WithGrepDisableSessionIsolation() FileGrepOption {
	return func(r *fileGrepRequest) { r.DisableSessionIsolation = true }
}

// WithGlobDisableSessionIsolation disables session isolation for FileGlob.
func WithGlobDisableSessionIsolation() FileGlobOption {
	return func(r *fileGlobRequest) { r.DisableSessionIsolation = true }
}

// WithListDisableSessionIsolation disables session isolation for FileList.
func WithListDisableSessionIsolation() FileListOption {
	return func(r *fileListRequest) { r.DisableSessionIsolation = true }
}

// FileUploadOption is a functional option for FileUpload.
type FileUploadOption func(*fileUploadOptions)

type fileUploadOptions struct {
	DisableSessionIsolation bool
	SkillsWritable          bool
}

// WithUploadDisableSessionIsolation disables session isolation for FileUpload.
func WithUploadDisableSessionIsolation() FileUploadOption {
	return func(o *fileUploadOptions) { o.DisableSessionIsolation = true }
}

// WithUploadSkillsWritable enables writing to the skills directory for FileUpload.
func WithUploadSkillsWritable() FileUploadOption {
	return func(o *fileUploadOptions) { o.SkillsWritable = true }
}

// FileDownloadOption is a functional option for FileDownload.
type FileDownloadOption func(*fileDownloadOptions)

type fileDownloadOptions struct {
	DisableSessionIsolation bool
}

// WithDownloadDisableSessionIsolation disables session isolation for FileDownload.
func WithDownloadDisableSessionIsolation() FileDownloadOption {
	return func(o *fileDownloadOptions) { o.DisableSessionIsolation = true }
}
