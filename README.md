# sandbox-container

A sandbox container service built with Go + Gin, providing isolated command execution, file operations, code execution, and skills management.

## Features

- **Bash Execution** — Execute bash commands in isolated sessions with streaming output, async mode, timeout control, and process interaction (stdin write/kill)
- **File Operations** — Full file management: read/write, search, glob/grep, directory listing, file upload/download, string replacement
- **Code Execution** — Run Python and JavaScript code with timeout control and pre-installed scientific computing and web development libraries
- **Skills Management** — Global skills store with CRUD operations, ZIP import, file management, and agent-level caching with version control
- **Session Isolation** — Directory isolation based on `agent_id` + `session_id` with TTL-based auto-cleanup and path traversal protection
- **Bwrap Sandbox** — Optional bubblewrap-based isolation with namespace separation (PID/UTS/IPC/network), read-only system mounts, and sandboxed file operations to prevent symlink escape attacks
- **Audit Logging** — Full request/response logging

## Quick Start

### Docker

```bash
docker build -t sandbox-container .

docker run -d \
  -p 9090:9090 \
  -v sandbox-data:/data/agents \
  -v sandbox-skills:/data/skills \
  -v sandbox-logs:/var/log/sandbox \
  sandbox-container
```

The server listens on port `9090`. Health check endpoint: `GET /v1/sandbox`.

### Local Development

```bash
go run .
```

## API Overview

### Sandbox Info

```
GET  /v1/sandbox                # Get sandbox environment info (OS, runtimes, tools)
GET  /v1/sandbox/packages/python # List installed Python packages
GET  /v1/sandbox/packages/nodejs # List installed Node.js packages
```

### Bash Execution

```
POST /v1/bash/exec              # Execute command
POST /v1/bash/output            # Read incremental output (streaming)
POST /v1/bash/write             # Write to stdin
POST /v1/bash/kill              # Kill command
GET  /v1/bash/sessions          # List bash sessions
POST /v1/bash/sessions/create   # Create persistent bash session
POST /v1/bash/sessions/:id/close # Close bash session
```

**Example:**

```json
POST /v1/bash/exec
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "command": "echo hello",
  "timeout": 30
}
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `agent_id` | string | Yes | Agent identifier |
| `session_id` | string | Yes | Session identifier |
| `command` | string | Yes | Bash command to execute |
| `exec_dir` | string | No | Working directory for command execution |
| `env` | map | No | Environment variables |
| `async_mode` | bool | No | Run command asynchronously (default: false) |
| `timeout` | float | No | Command timeout in seconds |
| `hard_timeout` | float | No | Hard kill timeout in seconds |
| `max_output_length` | int | No | Maximum output length |
| `env` | map | No | Environment variables for the runtime process |
| `enable_agent_workspace` | bool | No | Use the agent workspace directory instead of the session directory (default: false) |

### File Operations

```
POST /v1/file/read     # Read file
POST /v1/file/write    # Write file
POST /v1/file/replace  # String replacement
POST /v1/file/search   # Regex search file content
POST /v1/file/find     # Find files by glob pattern
POST /v1/file/grep     # Cross-file grep
POST /v1/file/glob     # Glob matching
POST /v1/file/list     # List directory contents
POST /v1/file/upload   # Upload file
GET  /v1/file/download # Download file
```

**Example:**

```json
POST /v1/file/write
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "file": "test.txt",
  "content": "hello world"
}
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `agent_id` | string | Yes | Agent identifier |
| `session_id` | string | Yes | Session identifier |
| `file` | string | Yes | File path |
| `disable_session_isolation` | bool | No | Use workspace directory instead of session directory (default: false) |
| `skills_writable` | bool | No | Allow writing to skills directories (default: false, applies to write/replace/upload) |

### Code Execution

```
POST /v1/code/execute  # Execute code (Python / JavaScript)
GET  /v1/code/info     # Get supported runtime info
```

**Example:**

```json
POST /v1/code/execute
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "language": "python",
  "code": "print('hello world')",
  "timeout": 30
}
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `agent_id` | string | Yes | Agent identifier |
| `session_id` | string | Yes | Session identifier |
| `language` | string | Yes | `python` or `javascript` |
| `code` | string | Yes | Source code to execute |
| `timeout` | int | No | Execution timeout in seconds |
| `cwd` | string | No | Working directory for execution |
| `env` | map | No | Environment variables for the runtime process |
| `enable_agent_workspace` | bool | No | Use the agent workspace directory instead of the session directory (default: false) |

### Skills Management

Skills are managed globally in `/data/skills/`. Each skill is identified by a unique name (letters, digits, hyphens only).

```
POST   /v1/skills/create        # Create an empty skill
POST   /v1/skills/get           # Get skill metadata
POST   /v1/skills/update        # Update skill description
POST   /v1/skills/rename        # Rename a skill
POST   /v1/skills/import        # Import skill from a ZIP URL
POST   /v1/skills/import/upload # Import skills from uploaded ZIP files (multipart)
POST   /v1/skills/list          # List all global skills
POST   /v1/skills/delete        # Delete a global skill
POST   /v1/skills/tree          # View skill directory tree
POST   /v1/skills/copy          # Copy a skill to a new name
GET    /v1/skills/export        # Export skill as ZIP download
POST   /v1/skills/file/read     # Read a file in a skill
POST   /v1/skills/file/write    # Write a file to a skill
POST   /v1/skills/file/update   # Replace string content in a skill file
POST   /v1/skills/file/mkdir    # Create a directory in a skill
POST   /v1/skills/file/delete   # Delete a file or directory in a skill
POST   /v1/skills/agents/:agent_id/list  # List agent skills (frontmatter summaries)
POST   /v1/skills/agents/:agent_id/load  # Load skills into agent session (body content)
DELETE /v1/skills/agents/:agent_id/cache # Clear agent skill cache
```

**Example — Create a skill:**

```json
POST /v1/skills/create
{
  "name": "my-skill",
  "description": "A useful skill"
}
```

**Example — Write a file to a skill:**

```json
POST /v1/skills/file/write
{
  "name": "my-skill",
  "path": "src/helper.py",
  "content": "def greet(): return 'hello'"
}
```

**Example — List agent skills:**

```json
POST /v1/skills/agents/agent-1/list
{
  "skill_ids": ["my-skill", "another-skill"]
}
```

**Example — Load skills into an agent session:**

```json
POST /v1/skills/agents/agent-1/load
{
  "skill_ids": ["my-skill", "another-skill"]
}
```

**Example — Load skills with writable mode (skips version sync, uses local copy):**

```json
POST /v1/skills/agents/agent-1/load
{
  "skill_ids": ["my-skill"],
  "skills_writable": true
}
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `skill_ids` | []string | Yes | List of skill IDs to list/load |
| `cleanup` | bool | No | Clean up stale skills (default: false) |
| `skills_writable` | bool | No | Skip version sync, use agent's local copy as-is (default: false) |

Skills are cached per-agent. When loaded, the system compares the version timestamp (`_meta.json`) — if the agent's cached copy is outdated, it's automatically updated from the global store. When `skills_writable` is true, this version sync is skipped.

### Session Management

```
GET    /v1/sessions                    # List all sessions for an agent
GET    /v1/sessions/:session_id/audits # Get paginated audit logs for a session
DELETE /v1/sessions/:session_id        # Delete a session and its audit logs
```

**Example — List sessions:**

```
GET /v1/sessions?agent_id=agent-1
```

**Example — Get audit logs:**

```
GET /v1/sessions/session-1/audits?agent_id=agent-1&offset=0&limit=100
```

## Go Client

```go
import "github.com/hyponet/sandbox-container/client"

c := client.NewClient("http://localhost:9090")

// Execute bash command
result, _ := c.BashExec("agent-1", "session-1", "ls -la",
    client.WithTimeout(30),
    client.WithEnv(map[string]string{"FOO": "bar"}))

// Execute code
result, _ := c.CodeExecute("agent-1", "session-1", "python",
    "print('hello')",
    client.WithCodeTimeout(30),
    client.WithCodeEnv(map[string]string{"GREETING": "hello"}))

// File operations
content, _ := c.FileRead("agent-1", "session-1", "/workspace/main.go",
    client.WithLineRange(0, 100))
c.FileWrite("agent-1", "session-1", "test.txt", "hello")
files, _ := c.FileGlob("agent-1", "session-1", "/", "**/*.go")

// Skills — Global management
c.SkillCreate("my-skill", "A useful skill")
c.SkillGet("my-skill")
c.SkillUpdate("my-skill", "Updated description")
c.SkillRename("my-skill", "new-name")
c.SkillCopy("new-name", "copied-skill")
c.SkillImport("imported-skill", "https://example.com/skill.zip")
skills, _ := c.SkillList()
tree, _ := c.SkillTree("new-name")
zipReader, _ := c.SkillExport("new-name")
c.SkillFileWrite("new-name", "src/helper.py", "def greet(): pass")
c.SkillFileMkdir("new-name", "src/utils")
c.SkillFileDelete("new-name", "src/helper.py")
c.SkillImportUpload([]client.SkillUploadEntry{
    {Name: "uploaded-skill", ZipPath: "/tmp/skill.zip"},
})
c.SkillDelete("new-name")

// Skills — Agent-level operations
loaded, _ := c.SkillAgentLoad("agent-1", []string{"my-skill"})
listed, _ := c.SkillAgentList("agent-1", []string{"my-skill"})
c.SkillAgentCacheDelete("agent-1", "my-skill")

// Session management
sessions, _ := c.SessionList("agent-1")
audits, _ := c.SessionGetAuditLogs("agent-1", "session-1", 0, 100)
c.SessionDelete("agent-1", "session-1")
```

## Session Isolation

Each `agent_id` + `session_id` pair maps to an independent directory:

```
/data/
  skills/                         # Global skills store
    <skill-id>/                   # Skill ID (letters, digits, hyphens)
      _meta.json                  # System-maintained metadata (version timestamps)
      SKILLS.md                   # Skill documentation
      ...                         # Skill files
  agents/
    <agent_id>/
      skills/                     # Agent-level skill cache (copied from global)
        <skill-id>/
      workspace/                  # Persistent workspace (used when disable_session_isolation=true)
        skills -> ../skills       # Symlink to agent's skills cache
      sessions/
        <session_id>/             # Session working directory
          skills -> ../../skills  # Symlink to agent's skills cache
```

- Default TTL: 24 hours
- Cleanup interval: 10 minutes
- Path traversal protection: paths containing `..` are rejected

## Bwrap Isolation

When `SANDBOX_ISOLATION_MODE=bwrap` is set, all command execution and file operations run inside [bubblewrap](https://github.com/containers/bubblewrap) sandboxes. This provides OS-level isolation on top of the session directory isolation.

### Security Features

- **Namespace isolation** — PID, UTS, IPC namespaces are always unshared; network namespace optionally unshared via `SANDBOX_BWRAP_NETWORK=isolated`
- **Read-only system mounts** — `/usr`, `/lib`, `/lib64`, `/bin`, `/sbin`, `/etc` are mounted read-only
- **Sandboxed file operations** — File reads/writes go through `BwrapFileOperator`, which executes all file I/O inside bwrap using base64-encoded stdin/stdout, preventing symlink escape attacks
- **Ephemeral `/tmp`** — Each command gets a fresh tmpfs `/tmp`
- **Skills read-only** — Skills directories are mounted read-only inside the sandbox
- **Process safety** — `--die-with-parent` and `--new-session` prevent orphaned processes

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SANDBOX_ISOLATION_MODE` | Set to `bwrap` to enable bubblewrap isolation | `none` |
| `SANDBOX_BWRAP_NETWORK` | `host` (allow network) or `isolated` (no network) | `host` |
| `SANDBOX_BWRAP_EXTRA_RO_BINDS` | Comma-separated additional read-only bind mount paths | — |
| `SANDBOX_BWRAP_PROC_BIND` | Set any value to use `--bind /proc /proc` instead of `--proc /proc` (for restricted systems) | — |

### Example

```bash
docker run -d \
  -p 9090:9090 \
  -e SANDBOX_ISOLATION_MODE=bwrap \
  -e SANDBOX_BWRAP_NETWORK=isolated \
  -v sandbox-data:/data/agents \
  -v sandbox-skills:/data/skills \
  -v sandbox-logs:/var/log/sandbox \
  sandbox-container
```

### Runtime Path Resolution

Bwrap mode automatically detects and mounts runtime paths needed by commands. Paths under `/usr/local`, `/opt`, `/run/current-system`, and `/nix/store` are auto-mounted read-only when a command binary resides there.

## Workspace Mode

By default, all file and command operations are scoped to a session-specific directory under `/data/agents/<agent_id>/sessions/<session_id>/`. Each session gets a fresh, isolated filesystem that is automatically cleaned up after the TTL expires.

When `disable_session_isolation` is set to `true`, the request resolves paths against a **persistent workspace directory** instead of the session directory:

```
/data/agents/<agent_id>/workspace/
```

Files in the workspace persist across sessions and are **not** subject to TTL-based cleanup. This is useful for long-lived agent workflows that need to maintain state between sessions.

### How It Works

- When `disable_session_isolation` is `false` (default), paths resolve under the session directory as usual.
- When `disable_session_isolation` is `true`, non-skills paths resolve under `/data/agents/<agent_id>/workspace/`. Skills paths (`/skills/...`) continue to resolve to the agent's skills cache directory.
- The workspace directory is created on first access with a `skills` symlink pointing to the agent's skills cache.

### Supported Endpoints

The `disable_session_isolation` parameter is available on the following endpoints:

| Endpoint Group | Endpoints |
|----------------|-----------|
| File | `read`, `write`, `replace`, `search`, `find`, `grep`, `glob`, `list`, `upload`, `download` |
| Bash | `exec`, `sessions/create` |
| Code | `execute` |

### Example

```json
POST /v1/file/write
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "file": "/project/src/main.py",
  "content": "print('persistent')",
  "disable_session_isolation": true
}
```

The file is written to `/data/agents/agent-1/workspace/project/src/main.py` and can be read back from any session with `disable_session_isolation: true`.

```json
POST /v1/bash/exec
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "command": "ls /project/src/",
  "disable_session_isolation": true
}
```

The command runs with the workspace directory as the root for path resolution.

## Skills Writable Mode

By default, the skills directory (`/skills/...`) is read-only for file API endpoints. Write operations to paths under `/skills/` are rejected with a `403 Forbidden` error. This protects the agent's skill cache from accidental modification.

When `skills_writable` is set to `true`, the file APIs allow writing to skills directories, and the agent skill list/load endpoints skip version sync checking.

### How It Works

- **File APIs (`write`, `replace`, `upload`):** When `skills_writable` is `true`, paths targeting `/skills/...` are accepted. Without it, such requests return `403`.
- **Agent skill list/load:** When `skills_writable` is `true`, the system skips the version comparison against the global skills store and uses the agent's local copy as-is. This allows the agent to modify its cached skills without the changes being overwritten by the global sync.

### Supported Endpoints

| Endpoint | Parameter | Behavior |
|----------|-----------|----------|
| `POST /v1/file/write` | `skills_writable` | Allow writing to `/skills/...` paths |
| `POST /v1/file/replace` | `skills_writable` | Allow string replacement in `/skills/...` paths |
| `POST /v1/file/upload` | `skills_writable` | Allow file uploads to `/skills/...` paths |
| `POST /v1/skills/agents/:agent_id/list` | `skills_writable` | Skip version sync, use local copy |
| `POST /v1/skills/agents/:agent_id/load` | `skills_writable` | Skip version sync, use local copy |

### Example — Write to a skills file

```json
POST /v1/file/write
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "file": "/skills/my-skill/config.json",
  "content": "{\"enabled\": true}",
  "skills_writable": true
}
```

### Example — Load skills with writable mode

```json
POST /v1/skills/agents/agent-1/load
{
  "skill_ids": ["my-skill"],
  "skills_writable": true
}
```

This loads the skill from the agent's local cache without checking or syncing from the global store.

## Pre-installed Environment

Based on Ubuntu 22.04, pre-installed with:

- **Python** 3.10 / 3.11 / 3.12 + scientific computing libraries (numpy, pandas, scipy, matplotlib, opencv, etc.)
- **Node.js** 22.x
- **System tools** — git, curl, wget, vim, jq, ripgrep, cmake, build-essential, etc.
- **uv** — High-speed Python package manager

## Audit Logging

All requests are logged to `/var/log/sandbox/audit.log`, including timestamp, request method/path/body, response status/body, latency, and client IP.
