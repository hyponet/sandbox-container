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
	if ur.EnableAgentWorkspace {
		w.WriteField("enable_agent_workspace", "true")
	}
	if ur.UserID != "" {
		w.WriteField("user_id", ur.UserID)
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
	if fd.EnableAgentWorkspace {
		params.Set("enable_agent_workspace", "true")
	}
	if fd.UserID != "" {
		params.Set("user_id", fd.UserID)
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
	EnableAgentWorkspace bool   `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
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
	EnableAgentWorkspace bool   `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
}

type fileReplaceRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	File      string `json:"file"`
	OldStr    string `json:"old_str"`
	NewStr    string `json:"new_str"`
	EnableAgentWorkspace bool   `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
}

type fileSearchRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	File      string `json:"file"`
	Regex     string `json:"regex"`
	EnableAgentWorkspace bool   `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
}

type fileFindRequest struct {
	AgentID   string `json:"agent_id"`
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Glob      string `json:"glob"`
	EnableAgentWorkspace bool   `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
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
	EnableAgentWorkspace bool     `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
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
	EnableAgentWorkspace bool     `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
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
	EnableAgentWorkspace bool     `json:"enable_agent_workspace"`
	UserID              string `json:"user_id,omitempty"`
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

// --- Agent Workspace Options ---

// WithFileReadAgentWorkspace enables agent workspace mode for FileRead.
func WithFileReadAgentWorkspace() FileReadOption {
	return func(r *fileReadRequest) { r.EnableAgentWorkspace = true }
}

// WithFileWriteAgentWorkspace enables agent workspace mode for FileWrite.
func WithFileWriteAgentWorkspace() FileWriteOption {
	return func(r *fileWriteRequest) { r.EnableAgentWorkspace = true }
}

// FileReplaceOption is a functional option for FileReplace.
type FileReplaceOption func(*fileReplaceRequest)

// WithFileReplaceAgentWorkspace enables agent workspace mode for FileReplace.
func WithFileReplaceAgentWorkspace() FileReplaceOption {
	return func(r *fileReplaceRequest) { r.EnableAgentWorkspace = true }
}

// FileSearchOption is a functional option for FileSearch.
type FileSearchOption func(*fileSearchRequest)

// WithFileSearchAgentWorkspace enables agent workspace mode for FileSearch.
func WithFileSearchAgentWorkspace() FileSearchOption {
	return func(r *fileSearchRequest) { r.EnableAgentWorkspace = true }
}

// FileFindOption is a functional option for FileFind.
type FileFindOption func(*fileFindRequest)

// WithFileFindAgentWorkspace enables agent workspace mode for FileFind.
func WithFileFindAgentWorkspace() FileFindOption {
	return func(r *fileFindRequest) { r.EnableAgentWorkspace = true }
}

// WithGrepAgentWorkspace enables agent workspace mode for FileGrep.
func WithGrepAgentWorkspace() FileGrepOption {
	return func(r *fileGrepRequest) { r.EnableAgentWorkspace = true }
}

// WithGlobAgentWorkspace enables agent workspace mode for FileGlob.
func WithGlobAgentWorkspace() FileGlobOption {
	return func(r *fileGlobRequest) { r.EnableAgentWorkspace = true }
}

// WithListAgentWorkspace enables agent workspace mode for FileList.
func WithListAgentWorkspace() FileListOption {
	return func(r *fileListRequest) { r.EnableAgentWorkspace = true }
}

// FileUploadOption is a functional option for FileUpload.
type FileUploadOption func(*fileUploadOptions)

type fileUploadOptions struct {
	EnableAgentWorkspace bool
	UserID              string
}

// WithUploadAgentWorkspace enables agent workspace mode for FileUpload.
func WithUploadAgentWorkspace() FileUploadOption {
	return func(o *fileUploadOptions) { o.EnableAgentWorkspace = true }
}

// FileDownloadOption is a functional option for FileDownload.
type FileDownloadOption func(*fileDownloadOptions)

type fileDownloadOptions struct {
	EnableAgentWorkspace bool
	UserID              string
}

// WithDownloadAgentWorkspace enables agent workspace mode for FileDownload.
func WithDownloadAgentWorkspace() FileDownloadOption {
	return func(o *fileDownloadOptions) { o.EnableAgentWorkspace = true }
}

// WithFileReadUserID sets the user ID for userdata access in FileRead.
func WithFileReadUserID(userID string) FileReadOption {
	return func(r *fileReadRequest) { r.UserID = userID }
}

// WithFileWriteUserID sets the user ID for userdata access in FileWrite.
func WithFileWriteUserID(userID string) FileWriteOption {
	return func(r *fileWriteRequest) { r.UserID = userID }
}

// WithFileReplaceUserID sets the user ID for userdata access in FileReplace.
func WithFileReplaceUserID(userID string) FileReplaceOption {
	return func(r *fileReplaceRequest) { r.UserID = userID }
}

// WithFileSearchUserID sets the user ID for userdata access in FileSearch.
func WithFileSearchUserID(userID string) FileSearchOption {
	return func(r *fileSearchRequest) { r.UserID = userID }
}

// WithFileFindUserID sets the user ID for userdata access in FileFind.
func WithFileFindUserID(userID string) FileFindOption {
	return func(r *fileFindRequest) { r.UserID = userID }
}

// WithFileGrepUserID sets the user ID for userdata access in FileGrep.
func WithFileGrepUserID(userID string) FileGrepOption {
	return func(r *fileGrepRequest) { r.UserID = userID }
}

// WithFileGlobUserID sets the user ID for userdata access in FileGlob.
func WithFileGlobUserID(userID string) FileGlobOption {
	return func(r *fileGlobRequest) { r.UserID = userID }
}

// WithFileListUserID sets the user ID for userdata access in FileList.
func WithFileListUserID(userID string) FileListOption {
	return func(r *fileListRequest) { r.UserID = userID }
}

// WithFileUploadUserID sets the user ID for userdata access in FileUpload.
func WithFileUploadUserID(userID string) FileUploadOption {
	return func(o *fileUploadOptions) { o.UserID = userID }
}

// WithFileDownloadUserID sets the user ID for userdata access in FileDownload.
func WithFileDownloadUserID(userID string) FileDownloadOption {
	return func(o *fileDownloadOptions) { o.UserID = userID }
}
