// Package client provides a Go SDK for the sandbox-container API.
//
// Response types in this file mirror the server's model/types.go JSON structure.
// When the server model changes, corresponding types here must be updated to match.
package client

import "time"

// Error represents an API error response.
type Error struct {
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return e.Message
}

// =============================================
// Common response wrapper
// =============================================

type apiResponse struct {
	Success bool        `json:"success"`
	Message *string     `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
	Hint    *string     `json:"hint,omitempty"`
}

// =============================================
// Sandbox types
// =============================================

type SandboxResponse struct {
	HomeDir   string        `json:"home_dir"`
	Workspace *string       `json:"workspace,omitempty"`
	Version   string        `json:"version"`
	Detail    SandboxDetail `json:"detail"`
}

type SandboxDetail struct {
	System  SystemEnv      `json:"system"`
	Runtime RuntimeEnv     `json:"runtime"`
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

// PackageInfo represents an installed package entry returned by the sandbox packages endpoints.
// Fields are loosely typed since the exact shape depends on the package manager (pip/npm).
type PackageInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// =============================================
// Bash types
// =============================================

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

type BashExecResult struct {
	SessionID    string        `json:"session_id"`
	CommandID    string        `json:"command_id"`
	Command      string        `json:"command"`
	Status       CommandStatus `json:"status"`
	Stdout       *string       `json:"stdout,omitempty"`
	Stderr       *string       `json:"stderr,omitempty"`
	ExitCode     *int          `json:"exit_code,omitempty"`
	Offset       int           `json:"offset"`
	StderrOffset int           `json:"stderr_offset"`
}

type BashOutputResult struct {
	SessionID    string           `json:"session_id"`
	Stdout       string           `json:"stdout"`
	Stderr       string           `json:"stderr"`
	Offset       int              `json:"offset"`
	StderrOffset int              `json:"stderr_offset"`
	Command      *BashCommandInfo `json:"command,omitempty"`
}

type BashCommandInfo struct {
	CommandID string        `json:"command_id"`
	Command   string        `json:"command"`
	Status    CommandStatus `json:"status"`
	ExitCode  *int          `json:"exit_code,omitempty"`
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

// =============================================
// File types
// =============================================

type FileReadResult struct {
	Content string `json:"content"`
	File    string `json:"file"`
}

type FileWriteResult struct {
	File         string `json:"file"`
	BytesWritten *int   `json:"bytes_written,omitempty"`
}

type FileReplaceResult struct {
	File          string `json:"file"`
	ReplacedCount int    `json:"replaced_count"`
}

type FileSearchResult struct {
	File        string   `json:"file"`
	Matches     []string `json:"matches"`
	LineNumbers []int    `json:"line_numbers"`
}

type FileFindResult struct {
	Path  string   `json:"path"`
	Files []string `json:"files"`
}

type FileGrepResult struct {
	Path          string      `json:"path"`
	Pattern       string      `json:"pattern"`
	Matches       []GrepMatch `json:"matches"`
	MatchCount    int         `json:"match_count"`
	FilesSearched *int        `json:"files_searched,omitempty"`
	FilesMatched  *int        `json:"files_matched,omitempty"`
	Truncated     bool        `json:"truncated"`
}

type GrepMatch struct {
	File          string   `json:"file"`
	LineNumber    int      `json:"line_number"`
	LineContent   string   `json:"line_content"`
	ContextBefore []string `json:"context_before,omitempty"`
	ContextAfter  []string `json:"context_after,omitempty"`
}

type FileGlobResult struct {
	Path       string         `json:"path"`
	Pattern    string         `json:"pattern"`
	Files      []GlobFileInfo `json:"files"`
	TotalCount int            `json:"total_count"`
	Truncated  bool           `json:"truncated"`
}

type GlobFileInfo struct {
	Path         string  `json:"path"`
	Name         string  `json:"name"`
	IsDirectory  bool    `json:"is_directory"`
	Size         *int64  `json:"size,omitempty"`
	ModifiedTime *string `json:"modified_time,omitempty"`
}

type FileListResult struct {
	Path           string     `json:"path"`
	Files          []FileInfo `json:"files"`
	TotalCount     int        `json:"total_count"`
	DirectoryCount int        `json:"directory_count"`
	FileCount      int        `json:"file_count"`
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

type FileUploadResult struct {
	FilePath string `json:"file_path"`
	FileSize int64  `json:"file_size"`
	Success  bool   `json:"success"`
}

// =============================================
// Code types
// =============================================

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
// Skill types
// =============================================

type SkillMetaJSON struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

type SkillCreateResult struct {
	Skill SkillMetaJSON `json:"skill"`
}

type SkillImportResult struct {
	Skill SkillMetaJSON `json:"skill"`
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

type SkillFileReadResult struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

type SkillFileWriteResult struct {
	Path         string `json:"path"`
	BytesWritten int    `json:"bytes_written"`
}

type SkillFileUpdateResult struct {
	Path          string `json:"path"`
	ReplacedCount int    `json:"replaced_count"`
}

type SkillFileMkdirResult struct {
	Path string `json:"path"`
}

type SkillFileDeleteResult struct {
	Path string `json:"path"`
}

type SkillImportUploadResult struct {
	Skills []SkillMetaJSON `json:"skills"`
}

type SkillGlobalListResult struct {
	Skills []SkillMetaJSON `json:"skills"`
}

type SkillSummary struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Frontmatter string `json:"frontmatter"`
}

type AgentSkillListResult struct {
	Skills []SkillSummary `json:"skills"`
}

type SkillContent struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type AgentSkillLoadResult struct {
	Skills []SkillContent `json:"skills"`
}

// =============================================
// Session management types
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
