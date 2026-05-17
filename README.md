# sandbox-container

A sandbox container service built with Go + Gin, providing isolated command execution, file operations, code execution, and skills management.

## Features

- **Bash Execution** — Execute bash commands in isolated sessions with streaming output, async mode, timeout control, and process interaction (stdin write/kill)
- **File Operations** — Full file management: read/write, search, glob/grep, directory listing, file upload/download, string replacement
- **Code Execution** — Run Python and JavaScript code with timeout control and pre-installed scientific computing and web development libraries
- **Skills Management** — Global skills store with CRUD operations, ZIP import, file management, and agent-level caching with version control
- **Session Isolation** — Directory isolation based on `agent_id` + `session_id` with TTL-based auto-cleanup and path traversal protection
- **Userdata** — Per-user persistent directory (`/data/users/<user_id>/`) mounted to `/home`, enabling data sharing across agents for the same user
- **Projectdata** — Per-project persistent directory (`/data/projects/<project_id>/`) mounted to `/home/project`
- **Bwrap Sandbox** — Bubblewrap-based isolation by default, with namespace separation (PID/UTS/IPC/network), read-only system mounts, and sandboxed file operations to prevent symlink escape attacks
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

By default the service requires a working `bwrap` binary on the host.

## API Overview

### Sandbox Info

```
GET  /v1/sandbox                # Get sandbox environment info (OS, runtimes, tools)
GET  /v1/sandbox/packages/python # List installed Python packages
GET  /v1/sandbox/packages/nodejs # List installed Node.js packages
POST /v1/sandbox/fsinfo         # Get filesystem layout info (work_dir, skills dir, etc.)
```

**Example — Get filesystem info:**

```json
POST /v1/sandbox/fsinfo
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "enable_agent_workspace": false
}
```

Response:

```json
{
  "data": {
    "work_dir": "/home",
    "directories": {
      "skills": "/agents/skills"
    }
  }
}
```

When `user_id` is provided:

```json
POST /v1/sandbox/fsinfo
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "user_id": "user-123"
}
```

Response:

```json
{
  "data": {
    "work_dir": "/home",
    "directories": {
      "skills": "/agents/skills",
      "userdata": "/home"
    }
  }
}
```

> Note: The `skills` directory is always present at `/agents/skills`. The `userdata` directory only appears when `user_id` is provided and points to `/home`. The `projectdata` directory appears when `project_id` is provided and points to `/home/project`.

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
| `user_id` | string | No | User identifier for userdata mount; mounts `/data/users/<user_id>/` to `/home` (default: empty) |
| `project_id` | string | No | Project identifier for projectdata mount; mounts `/data/projects/<project_id>/` to `/home/project` when authorized (default: empty) |

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
| `file` | string | Yes | Logical file path. Bare paths resolve under `/home`; `/agents/skills/...` is read-only; `/home/project/...` accesses project data when `project_id` is provided |
| `enable_agent_workspace` | bool | No | Use the agent workspace directory instead of the session directory (default: false) |
| `user_id` | string | No | User identifier for userdata access. When provided, `/home` is backed by `/data/users/<user_id>/` |
| `project_id` | string | No | Project identifier for projectdata access. When provided, `/home/project` is backed by `/data/projects/<project_id>/` |

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
| `user_id` | string | No | User identifier for userdata mount; mounts `/data/users/<user_id>/` to `/home` (default: empty) |
| `project_id` | string | No | Project identifier for projectdata mount; mounts `/data/projects/<project_id>/` to `/home/project` when authorized (default: empty) |

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

**Example — Load skills from the current agent workspace cache (skips version sync):**

```json
POST /v1/skills/agents/agent-1/load
{
  "skill_ids": ["my-skill"],
  "enable_agent_workspace": true
}
```

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `skill_ids` | []string | Yes | List of skill IDs to list/load |
| `cleanup` | bool | No | Clean up stale skills (default: false) |
| `enable_agent_workspace` | bool | No | Skip version sync and use the agent's local cached copy as-is (default: false) |
| `user_id` | string | No | Enable the user skill layer at `/home/skills` with higher priority than agent-cached skills |

Skills are cached per-agent. In normal mode, the system compares the version timestamp (`_meta.json`) and refreshes outdated cached copies from the global store. When `enable_agent_workspace` is true, list/load use the existing local cache as-is and skip that sync step.

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

// Filesystem info
fsInfo, _ := c.GetFsInfo("agent-1", "session-1")
// fsInfo.WorkDir = "/home"
// fsInfo.Directories["skills"] = "/agents/skills"

// Filesystem info with userdata
fsInfo, _ := c.GetFsInfo("agent-1", "session-1", client.WithFsInfoUserID("user-123"))
// fsInfo.Directories["userdata"] = "/home"

// Filesystem info with authorized projectdata
fsInfo, _ = c.GetFsInfo("agent-1", "session-1", client.WithFsInfoProjectID("project-a"))
// fsInfo.Directories["projectdata"] = "/home/project"

// Bash/command execution with userdata mount
result, _ := c.BashExec("agent-1", "session-1", "cat /home/config.json",
    client.WithBashUserID("user-123"))

// Bash/command execution with authorized projectdata mount
result, _ = c.BashExec("agent-1", "session-1", "cat /home/project/shared/config.json",
    client.WithBashProjectID("project-a"))

// File read from userdata
content, _ := c.FileRead("agent-1", "session-1", "/config.json",
    client.WithFileReadUserID("user-123"))

// File read from authorized projectdata
projectContent, _ := c.FileRead("agent-1", "session-1", "/home/project/shared/config.json",
    client.WithFileReadProjectID("project-a"))

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
  users/
    <user_id>/                    # Per-user persistent data (userdata)
      ...                         # Shared across agents for the same user
  projects/
    <project_id>/                 # Per-project persistent data (projectdata)
      ...                         # Shared across authorized agents for the same project
  agents/
    <agent_id>/
      skills/                     # Agent-level skill cache (copied from global)
        <skill-id>/
      workspace/                  # Persistent workspace (used when enable_agent_workspace=true)
      sessions/
        <session_id>/             # Session working directory
```

- Default TTL: 24 hours
- Cleanup interval: 10 minutes
- Path traversal protection: paths containing `..` are rejected

## Bwrap Isolation

All command execution and file operations run inside [bubblewrap](https://github.com/containers/bubblewrap) sandboxes. This provides OS-level isolation on top of the session directory isolation.

### Path Remapping

In bwrap mode, the host filesystem paths are remapped inside the sandbox to hide the host directory structure:

| Host Path | Sandbox Path | Access |
|-----------|--------------|--------|
| Session or workspace root | `/` | Read-write |
| Session or workspace `home/` subtree | `/home` | Read-write |
| Agent skills cache | `/agents/skills` | Read-only |
| User persistent data (`/data/users/<user_id>/`) | `/home` | Read-write (overlays the session/workspace `home/` subtree when `user_id` is provided) |
| Project persistent data (`/data/projects/<project_id>/`) | `/home/project` | Read-write (only when authorized `project_id` is provided) |

This means code and commands inside the sandbox start in `/home`, bare file API paths resolve under `/home`, and agent skills are always exposed at `/agents/skills`. When `user_id` is provided, `/home` is backed by the user's persistent directory. When an authorized `project_id` is provided, project data is available at `/home/project`. Access is provided entirely through bind mounts.

### Security Features

- **Namespace isolation** — PID, UTS, IPC namespaces are always unshared; network namespace optionally unshared via `SANDBOX_BWRAP_NETWORK=isolated`
- **Read-only system mounts** — `/usr`, `/lib`, `/lib64`, `/bin`, `/sbin`, `/etc` are mounted read-only
- **Sandboxed file operations** — File reads/writes go through `BwrapFileOperator`, which executes all file I/O inside bwrap using base64-encoded stdin/stdout, preventing symlink escape attacks
- **Ephemeral `/tmp`** — Each command gets a fresh tmpfs `/tmp`
- **Skills read-only** — Agent skill directories are mounted read-only inside the sandbox at `/agents/skills`
- **Process safety** — `--die-with-parent` and `--new-session` prevent orphaned processes

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `SANDBOX_BWRAP_NETWORK` | `host` (allow network) or `isolated` (no network) | `host` |
| `SANDBOX_BWRAP_EXTRA_RO_BINDS` | Comma-separated additional read-only bind mount paths | — |
| `SANDBOX_BWRAP_PROC_BIND` | Set any value to use `--bind /proc /proc` instead of `--proc /proc` (for restricted systems) | — |

### Example

```bash
docker run -d \
  -p 9090:9090 \
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

When `enable_agent_workspace` is set to `true`, the request resolves paths against the agent's **persistent workspace directory** instead of the session directory:

```
/data/agents/<agent_id>/workspace/
```

Files in the workspace persist across sessions and are **not** subject to TTL-based cleanup. This is useful for long-lived agent workflows that need to maintain state between sessions.

### How It Works

- When `enable_agent_workspace` is `false` (default), paths resolve under the session directory as usual.
- When `enable_agent_workspace` is `true`, bare file API paths resolve under the agent workspace's `/home` subtree and commands/code run against the workspace-backed sandbox.
- Agent skill paths remain `/agents/skills/...` and stay read-only.

### Supported Endpoints

The `enable_agent_workspace` parameter is available on the following endpoints:

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
  "enable_agent_workspace": true
}
```

The file is written into the agent workspace and can be read back from any session that uses `enable_agent_workspace: true`.

```json
POST /v1/bash/exec
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "command": "ls /home/project/src/",
  "enable_agent_workspace": true
}
```

The command runs with the workspace directory as the root for path resolution.

## Userdata

Userdata provides a per-user persistent directory that is shared across all agents. When a request includes a `user_id`, the directory `/data/users/<user_id>/` is mounted to `/home` (read-write) inside the sandbox.

### How It Works

- The userdata directory is bind-mounted at `/home` at runtime.
- Bare file API paths such as `/config.json` resolve to the user's persistent directory.
- Names like `/userdata/...` are not reserved; unless a canonical special root is used, they behave like ordinary directories under `/home`.

### Supported Endpoints

The `user_id` parameter is available on the following endpoints:

| Endpoint Group | Endpoints |
|----------------|-----------|
| File | `read`, `write`, `replace`, `search`, `find`, `grep`, `glob`, `list`, `upload`, `download` |
| Bash | `exec`, `sessions/create` |
| Code | `execute` |
| Sandbox | `fsinfo` |

### Example — Write to userdata

```json
POST /v1/file/write
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "file": "/config.json",
  "content": "{\"theme\": \"dark\"}",
  "user_id": "user-123"
}
```

The file is written to `/data/users/user-123/config.json` and can be accessed from any agent with the same `user_id`.

### Example — Read userdata in bash

```json
POST /v1/bash/exec
{
  "agent_id": "agent-2",
  "session_id": "session-1",
  "command": "cat /home/config.json",
  "user_id": "user-123"
}
```

The command runs with the user's persistent directory mounted at `/home`, allowing cross-agent data sharing.

## Projectdata

Projectdata provides a per-project persistent directory shared across authorized requests. When a request includes an authorized `project_id`, `/data/projects/<project_id>/` is mounted read-write at `/home/project` inside the sandbox.

### Supported Endpoints

The `project_id` parameter is available on the following endpoints:

| Endpoint Group | Endpoints |
|----------------|-----------|
| File | `read`, `write`, `replace`, `search`, `find`, `grep`, `glob`, `list`, `upload`, `download` |
| Bash | `exec`, `sessions/create` |
| Code | `execute` |
| Sandbox | `fsinfo` |

### Example — Write to projectdata

```json
POST /v1/file/write
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "file": "/home/project/shared/config.json",
  "content": "{\"mode\": \"team\"}",
  "project_id": "project-a"
}
```

The file is written to `/data/projects/project-a/shared/config.json` and can be accessed by other authorized agents using the same `project_id`.

### Example — Read projectdata in bash

```json
POST /v1/bash/exec
{
  "agent_id": "agent-2",
  "session_id": "session-1",
  "command": "cat /home/project/shared/config.json",
  "project_id": "project-a"
}
```

Names like `/projectdata/...` are not reserved; unless a canonical special root is used, they behave like ordinary directories under `/home`.

## Agent Skill Cache

Agent skill files under `/agents/skills/...` are read-only for file APIs. Write, replace, and upload operations to that path return `403 Forbidden`. A path like `/skills/...` is not treated as a skills alias; it is just a regular directory path under `/home`.

### How It Works

- **File APIs:** `/agents/skills/...` is always read-only.
- **Agent skill list/load:** When `enable_agent_workspace` is `true`, the system skips version comparison against the global skills store and uses the agent's local cached copy as-is.

### Supported Endpoints

| Endpoint | Parameter | Behavior |
|----------|-----------|----------|
| `POST /v1/file/write` | — | Writing `/agents/skills/...` is rejected with `403` |
| `POST /v1/file/replace` | — | Replacing `/agents/skills/...` is rejected with `403` |
| `POST /v1/file/upload` | — | Uploading into `/agents/skills/...` is rejected with `403` |
| `POST /v1/skills/agents/:agent_id/list` | `enable_agent_workspace` | Skip version sync, use the current local cache |
| `POST /v1/skills/agents/:agent_id/load` | `enable_agent_workspace` | Skip version sync, use the current local cache |

### Example — Load skills from the current local cache

```json
POST /v1/skills/agents/agent-1/load
{
  "skill_ids": ["my-skill"],
  "enable_agent_workspace": true
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
