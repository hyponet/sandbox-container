package model

import "time"

// =============================================
// Common response wrapper
// =============================================

type APIResponse struct {
	Success bool        `json:"success"`
	Message *string     `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Hint    *string     `json:"hint,omitempty"`
}

func OkResponse(data interface{}) APIResponse {
	return APIResponse{Success: true, Data: data}
}

func OkMsg(msg string) APIResponse {
	return APIResponse{Success: true, Message: &msg}
}

func ErrResponse(msg string) APIResponse {
	return APIResponse{Success: false, Message: &msg}
}

// =============================================
// Sandbox APIs
// =============================================

type SandboxResponse struct {
	HomeDir   string        `json:"home_dir"`
	Workspace *string       `json:"workspace,omitempty"`
	Version   string        `json:"version"`
	Detail    SandboxDetail `json:"detail"`
}

type SandboxDetail struct {
	System  SystemEnv    `json:"system"`
	Runtime RuntimeEnv   `json:"runtime"`
	Utils   []ToolCategory `json:"utils"`
}

type SystemEnv struct {
	OS            string   `json:"os"`
	OSVersion     string   `json:"os_version"`
	Arch          string   `json:"arch"`
	User          string   `json:"user"`
	HomeDir       string   `json:"home_dir"`
	Workspace     *string  `json:"workspace,omitempty"`
	Timezone      string   `json:"timezone"`
	OccupiedPorts []string `json:"occupied_ports"`
}

type RuntimeEnv struct {
	Python []ToolSpec `json:"python"`
	NodeJS []ToolSpec `json:"nodejs"`
}

type ToolSpec struct {
	Ver   string   `json:"ver,omitempty"`
	Bin   string   `json:"bin,omitempty"`
	Alias []string `json:"alias,omitempty"`
}

type ToolCategory struct {
	Category string         `json:"category"`
	Tools    []AvailableTool `json:"tools"`
}

type AvailableTool struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// =============================================
// Bash APIs
// =============================================

type BashExecRequest struct {
	AgentID         string            `json:"agent_id" binding:"required"`
	SessionID       string            `json:"session_id" binding:"required"`
	Command         string            `json:"command" binding:"required"`
	ExecDir         *string           `json:"exec_dir,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	AsyncMode       bool              `json:"async_mode"`
	Timeout         *float64          `json:"timeout,omitempty"`
	HardTimeout     *float64          `json:"hard_timeout,omitempty"`
	MaxOutputLength int               `json:"max_output_length"`
}

type BashExecResult struct {
	SessionID    string         `json:"session_id"`
	CommandID    string         `json:"command_id"`
	Command      string         `json:"command"`
	Status       CommandStatus  `json:"status"`
	Stdout       *string        `json:"stdout,omitempty"`
	Stderr       *string        `json:"stderr,omitempty"`
	ExitCode     *int           `json:"exit_code,omitempty"`
	Offset       int            `json:"offset"`
	StderrOffset int            `json:"stderr_offset"`
}

type BashOutputRequest struct {
	AgentID      string  `json:"agent_id" binding:"required"`
	SessionID    string  `json:"session_id" binding:"required"`
	CommandID    *string `json:"command_id,omitempty"`
	Offset       int     `json:"offset"`
	StderrOffset int     `json:"stderr_offset"`
	Wait         bool    `json:"wait"`
	WaitTimeout  float64 `json:"wait_timeout"`
}

type BashOutputResult struct {
	SessionID    string          `json:"session_id"`
	Stdout       string          `json:"stdout"`
	Stderr       string          `json:"stderr"`
	Offset       int             `json:"offset"`
	StderrOffset int             `json:"stderr_offset"`
	Command      *BashCommandInfo `json:"command,omitempty"`
}

type BashWriteRequest struct {
	AgentID   string  `json:"agent_id" binding:"required"`
	SessionID string  `json:"session_id" binding:"required"`
	CommandID *string `json:"command_id,omitempty"`
	Input     string  `json:"input" binding:"required"`
}

type BashKillRequest struct {
	AgentID   string `json:"agent_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
	Signal    string `json:"signal"`
}

type BashSessionCreateRequest struct {
	AgentID    string  `json:"agent_id" binding:"required"`
	SessionID  string  `json:"session_id" binding:"required"`
	BashSID   *string `json:"bash_session_id,omitempty"`
	ExecDir   *string `json:"exec_dir,omitempty"`
}

type BashSessionCloseRequest struct {
	AgentID   string `json:"agent_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
}

type BashSessionInfo struct {
	SessionID      string        `json:"session_id"`
	Status         SessionStatus `json:"status"`
	WorkingDir     string        `json:"working_dir"`
	CreatedAt      time.Time     `json:"created_at"`
	LastUsedAt     time.Time     `json:"last_used_at"`
	CurrentCommand *string       `json:"current_command,omitempty"`
	CommandCount   int           `json:"command_count"`
}

type BashCommandInfo struct {
	CommandID string        `json:"command_id"`
	Command   string        `json:"command"`
	Status    CommandStatus `json:"status"`
	ExitCode  *int          `json:"exit_code,omitempty"`
}

type CommandStatus string

const (
	StatusPending   CommandStatus = "pending"
	StatusRunning   CommandStatus = "running"
	StatusCompleted CommandStatus = "completed"
	StatusTimedOut  CommandStatus = "timed_out"
	StatusKilled    CommandStatus = "killed"
)

type SessionStatus string

const (
	SessionReady  SessionStatus = "ready"
	SessionClosed SessionStatus = "closed"
)

// =============================================
// File APIs
// =============================================

type FileReadRequest struct {
	AgentID   string `json:"agent_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
	File      string `json:"file" binding:"required"`
	StartLine *int   `json:"start_line,omitempty"`
	EndLine   *int   `json:"end_line,omitempty"`
}

type FileReadResult struct {
	Content string `json:"content"`
	File    string `json:"file"`
}

type FileWriteRequest struct {
	AgentID         string `json:"agent_id" binding:"required"`
	SessionID       string `json:"session_id" binding:"required"`
	File            string `json:"file" binding:"required"`
	Content         string `json:"content" binding:"required"`
	Encoding        string `json:"encoding,omitempty"`
	Append          bool   `json:"append"`
	LeadingNewline  bool   `json:"leading_newline"`
	TrailingNewline bool   `json:"trailing_newline"`
}

type FileWriteResult struct {
	File        string `json:"file"`
	BytesWritten *int  `json:"bytes_written,omitempty"`
}

type FileReplaceRequest struct {
	AgentID   string `json:"agent_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
	File      string `json:"file" binding:"required"`
	OldStr    string `json:"old_str" binding:"required"`
	NewStr    string `json:"new_str" binding:"required"`
}

type FileReplaceResult struct {
	File          string `json:"file"`
	ReplacedCount int    `json:"replaced_count"`
}

type FileSearchRequest struct {
	AgentID   string `json:"agent_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
	File      string `json:"file" binding:"required"`
	Regex     string `json:"regex" binding:"required"`
}

type FileSearchResult struct {
	File        string   `json:"file"`
	Matches     []string `json:"matches"`
	LineNumbers []int    `json:"line_numbers"`
}

type FileFindRequest struct {
	AgentID   string `json:"agent_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
	Path      string `json:"path" binding:"required"`
	Glob      string `json:"glob" binding:"required"`
}

type FileFindResult struct {
	Path  string   `json:"path"`
	Files []string `json:"files"`
}

type FileGrepRequest struct {
	AgentID          string   `json:"agent_id" binding:"required"`
	SessionID        string   `json:"session_id" binding:"required"`
	Path             string   `json:"path" binding:"required"`
	Pattern          string   `json:"pattern" binding:"required"`
	Include          []string `json:"include,omitempty"`
	Exclude          []string `json:"exclude,omitempty"`
	CaseInsensitive  bool     `json:"case_insensitive"`
	FixedStrings     bool     `json:"fixed_strings"`
	ContextBefore    int      `json:"context_before"`
	ContextAfter     int      `json:"context_after"`
	MaxResults       int      `json:"max_results"`
	Recursive        *bool    `json:"recursive,omitempty"`
}

type FileGrepResult struct {
	Path          string       `json:"path"`
	Pattern       string       `json:"pattern"`
	Matches       []GrepMatch  `json:"matches"`
	MatchCount    int          `json:"match_count"`
	FilesSearched *int         `json:"files_searched,omitempty"`
	FilesMatched  *int         `json:"files_matched,omitempty"`
	Truncated     bool         `json:"truncated"`
}

type GrepMatch struct {
	File          string   `json:"file"`
	LineNumber    int      `json:"line_number"`
	LineContent   string   `json:"line_content"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
}

type FileGlobRequest struct {
	AgentID          string   `json:"agent_id" binding:"required"`
	SessionID        string   `json:"session_id" binding:"required"`
	Path             string   `json:"path" binding:"required"`
	Pattern          string   `json:"pattern" binding:"required"`
	Exclude          []string `json:"exclude,omitempty"`
	IncludeHidden    bool     `json:"include_hidden"`
	FilesOnly        *bool    `json:"files_only,omitempty"`
	IncludeMetadata  *bool    `json:"include_metadata,omitempty"`
	MaxResults       int      `json:"max_results"`
}

type FileGlobResult struct {
	Path        string         `json:"path"`
	Pattern     string         `json:"pattern"`
	Files       []GlobFileInfo `json:"files"`
	TotalCount  int            `json:"total_count"`
	Truncated   bool           `json:"truncated"`
}

type GlobFileInfo struct {
	Path         string  `json:"path"`
	Name         string  `json:"name"`
	IsDirectory  bool    `json:"is_directory"`
	Size         *int64  `json:"size,omitempty"`
	ModifiedTime *string `json:"modified_time,omitempty"`
}

type FileListRequest struct {
	AgentID            string   `json:"agent_id" binding:"required"`
	SessionID          string   `json:"session_id" binding:"required"`
	Path               string   `json:"path" binding:"required"`
	Recursive          bool     `json:"recursive"`
	ShowHidden         *bool    `json:"show_hidden,omitempty"`
	FileTypes          []string `json:"file_types,omitempty"`
	MaxDepth           *int     `json:"max_depth,omitempty"`
	IncludeSize        *bool    `json:"include_size,omitempty"`
	IncludePermissions *bool    `json:"include_permissions,omitempty"`
}

type FileListResult struct {
	Path           string    `json:"path"`
	Files          []FileInfo `json:"files"`
	TotalCount     int       `json:"total_count"`
	DirectoryCount int       `json:"directory_count"`
	FileCount      int       `json:"file_count"`
}

type FileInfo struct {
	Name         string  `json:"name"`
	Path         string  `json:"path"`
	IsDirectory  bool    `json:"is_directory"`
	Size         *int64  `json:"size,omitempty"`
	ModifiedTime *string `json:"modified_time,omitempty"`
	Permissions  *string `json:"permissions,omitempty"`
	Extension    *string `json:"extension,omitempty"`
}

type FileUploadRequest struct {
	AgentID   string `form:"agent_id" binding:"required"`
	SessionID string `form:"session_id" binding:"required"`
	Path      string `form:"path" binding:"required"`
}

type FileUploadResult struct {
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
	Success  bool   `json:"success"`
}

// =============================================
// Code APIs
// =============================================

type CodeExecuteRequest struct {
	AgentID   string  `json:"agent_id" binding:"required"`
	SessionID string  `json:"session_id" binding:"required"`
	Language  string  `json:"language" binding:"required"`
	Code      string  `json:"code" binding:"required"`
	Timeout   *int    `json:"timeout,omitempty"`
	Cwd       *string `json:"cwd,omitempty"`
}

type CodeExecuteResponse struct {
	Language  string        `json:"language"`
	Status    string        `json:"status"`
	Outputs   []interface{} `json:"outputs"`
	Code      string        `json:"code"`
	Stdout    *string       `json:"stdout,omitempty"`
	Stderr    *string       `json:"stderr,omitempty"`
	ExitCode  *int          `json:"exit_code,omitempty"`
	Traceback []string      `json:"traceback,omitempty"`
}

type CodeInfoResponse struct {
	Languages []CodeLanguageInfo `json:"languages"`
}

type CodeLanguageInfo struct {
	Language       string                 `json:"language"`
	Description    string                 `json:"description"`
	RuntimeVersion *string                `json:"runtime_version,omitempty"`
	DefaultTimeout int                    `json:"default_timeout"`
	MaxTimeout     int                    `json:"max_timeout"`
	Details        map[string]interface{} `json:"details,omitempty"`
}

// =============================================
// Skills APIs
// =============================================

// SkillMetaJSON represents the _meta.json file stored in each global skill directory.
type SkillMetaJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// SkillCreateRequest creates a new empty skill in the global store.
type SkillCreateRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type SkillCreateResult struct {
	Skill SkillMetaJSON `json:"skill"`
}

// SkillImportRequest imports a skill from a ZIP URL into the global store.
type SkillImportRequest struct {
	Name   string `json:"name" binding:"required"`
	ZipURL string `json:"zip_url" binding:"required"`
}

type SkillImportResult struct {
	Skill SkillMetaJSON `json:"skill"`
}

// SkillTreeRequest returns the directory tree of a global skill.
type SkillTreeRequest struct {
	Name string `json:"name" binding:"required"`
}

type SkillFileEntry struct {
	Path        string `json:"path"`
	IsDirectory bool   `json:"is_directory"`
	Size        int64  `json:"size"`
}

type SkillTreeResult struct {
	Name  string           `json:"name"`
	Files []SkillFileEntry `json:"files"`
}

// SkillFileReadRequest reads a file within a global skill.
type SkillFileReadRequest struct {
	Name string `json:"name" binding:"required"`
	Path string `json:"path" binding:"required"`
}

type SkillFileReadResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// SkillFileWriteRequest writes a file within a global skill.
type SkillFileWriteRequest struct {
	Name    string `json:"name" binding:"required"`
	Path    string `json:"path" binding:"required"`
	Content string `json:"content"`
}

type SkillFileWriteResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

// SkillFileUpdateRequest replaces string content in a file within a global skill.
type SkillFileUpdateRequest struct {
	Name   string `json:"name" binding:"required"`
	Path   string `json:"path" binding:"required"`
	OldStr string `json:"old_str" binding:"required"`
	NewStr string `json:"new_str" binding:"required"`
}

type SkillFileUpdateResult struct {
	Path          string `json:"path"`
	ReplacedCount int    `json:"replaced_count"`
}

// SkillFileMkdirRequest creates a directory within a global skill.
type SkillFileMkdirRequest struct {
	Name string `json:"name" binding:"required"`
	Path string `json:"path" binding:"required"`
}

type SkillFileMkdirResult struct {
	Path string `json:"path"`
}

// SkillFileDeleteRequest deletes a file or directory within a global skill.
type SkillFileDeleteRequest struct {
	Name string `json:"name" binding:"required"`
	Path string `json:"path" binding:"required"`
}

type SkillFileDeleteResult struct {
	Path string `json:"path"`
}

// SkillImportUploadResult is the response for the upload-based import endpoint.
type SkillImportUploadResult struct {
	Skills []SkillMetaJSON `json:"skills"`
}

// SkillGlobalListResult lists all skills in the global store.
type SkillGlobalListResult struct {
	Skills []SkillMetaJSON `json:"skills"`
}

// SkillDeleteRequest deletes a global skill.
type SkillDeleteRequest struct {
	Name string `json:"name" binding:"required"`
}

// SkillGetRequest retrieves a single skill's details.
type SkillGetRequest struct {
	Name string `json:"name" binding:"required"`
}

type SkillGetResult struct {
	Skill       SkillMetaJSON `json:"skill"`
	Frontmatter string        `json:"frontmatter"`
	Body        string        `json:"body"`
}

// SkillUpdateRequest updates a skill's metadata.
type SkillUpdateRequest struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

type SkillUpdateResult struct {
	Skill SkillMetaJSON `json:"skill"`
}

// SkillRenameRequest renames a skill.
type SkillRenameRequest struct {
	Name    string `json:"name" binding:"required"`
	NewName string `json:"new_name" binding:"required"`
}

type SkillRenameResult struct {
	Skill SkillMetaJSON `json:"skill"`
}

// SkillCopyRequest copies a skill.
type SkillCopyRequest struct {
	Name    string `json:"name" binding:"required"`
	NewName string `json:"new_name" binding:"required"`
}

type SkillCopyResult struct {
	Skill SkillMetaJSON `json:"skill"`
}

// AgentSkillCacheDeleteResult is the response for agent cache deletion.
type AgentSkillCacheDeleteResult struct {
	Deleted []string `json:"deleted"`
}

// AgentSkillRequest is the body for agent skill list/load endpoints.
// agent_id comes from the URL path parameter.
type AgentSkillRequest struct {
	SkillIDs []string `json:"skill_ids" binding:"required"`
	Cleanup  bool     `json:"cleanup"`
}

// SkillSummary is returned by the agent list endpoint with frontmatter metadata.
type SkillSummary struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Frontmatter string `json:"frontmatter"`
}

// AgentSkillListResult is the response for the agent list endpoint.
type AgentSkillListResult struct {
	Skills []SkillSummary `json:"skills"`
}

// SkillContent holds the body (post-frontmatter) of a SKILLS.md.
type SkillContent struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// AgentSkillLoadResult is the response for the agent load endpoint.
type AgentSkillLoadResult struct {
	Skills []SkillContent `json:"skills"`
}

// =============================================
// Session Management APIs
// =============================================

type SessionInfo struct {
	SessionID    string `json:"session_id"`
	AgentID      string `json:"agent_id"`
	LastAccess   string `json:"last_access,omitempty"`
	AuditEntries int    `json:"audit_entries"`
}

type SessionListResult struct {
	Sessions []SessionInfo `json:"sessions"`
	Total    int           `json:"total"`
}

// AuditEntry is the canonical audit log record used for both writing and reading.
type AuditEntry struct {
	Timestamp   string            `json:"timestamp"`
	AgentID     string            `json:"agent_id,omitempty"`
	SessionID   string            `json:"session_id,omitempty"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers,omitempty"`
	RequestBody interface{}       `json:"request_body,omitempty"`
	Status      int               `json:"status"`
	Response    interface{}       `json:"response,omitempty"`
	Latency     string            `json:"latency"`
	ClientIP    string            `json:"client_ip"`
}

type AuditLogResult struct {
	SessionID string       `json:"session_id"`
	AgentID   string       `json:"agent_id"`
	Entries   []AuditEntry `json:"entries"`
	Total     int          `json:"total"`
	Offset    int          `json:"offset"`
	Limit     int          `json:"limit"`
}
