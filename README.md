# sandbox-container

A sandbox container service built with Go + Gin, providing isolated command execution, file operations, code execution, and skills management.

## Features

- **Bash Execution** — Execute bash commands in isolated sessions with streaming output, async mode, timeout control, and process interaction (stdin write/kill)
- **File Operations** — Full file management: read/write, search, glob/grep, directory listing, file upload/download, string replacement
- **Code Execution** — Run Python and JavaScript code with timeout control and pre-installed scientific computing and web development libraries
- **Skills Management** — Global skills store with CRUD operations, ZIP import, file management, and agent-level caching with version control
- **Session Isolation** — Directory isolation based on `agent_id` + `session_id` with TTL-based auto-cleanup and path traversal protection
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

### Skills Management

Skills are managed globally in `/data/skills/`. Each skill is identified by a unique name (letters, digits, hyphens only).

```
POST /v1/skills/create        # Create an empty skill
POST /v1/skills/import        # Import skill from a ZIP URL
POST /v1/skills/list          # List all global skills
POST /v1/skills/delete        # Delete a global skill
POST /v1/skills/tree          # View skill directory tree
POST /v1/skills/file/read     # Read a file in a skill
POST /v1/skills/file/write    # Write a file to a skill
POST /v1/skills/file/update   # Replace string content in a skill file
POST /v1/skills/file/mkdir    # Create a directory in a skill
POST /v1/skills/agents/:agent_id/list  # List agent skills (frontmatter summaries)
POST /v1/skills/agents/:agent_id/load  # Load skills into agent session (body content)
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

Skills are cached per-agent. When loaded, the system compares the version timestamp (`_meta.json`) — if the agent's cached copy is outdated, it's automatically updated from the global store.

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
    "print('hello')", client.WithCodeTimeout(30))

// File operations
content, _ := c.FileRead("agent-1", "session-1", "/workspace/main.go",
    client.WithLineRange(0, 100))
c.FileWrite("agent-1", "session-1", "test.txt", "hello")
files, _ := c.FileGlob("agent-1", "session-1", "/", "**/*.go")

// Skills — Global management
c.SkillCreate("my-skill", "A useful skill")
c.SkillImport("imported-skill", "https://example.com/skill.zip")
skills, _ := c.SkillList()
tree, _ := c.SkillTree("my-skill")
c.SkillFileWrite("my-skill", "src/helper.py", "def greet(): pass")
c.SkillFileMkdir("my-skill", "src/utils")
c.SkillDelete("my-skill")

// Skills — Load into agent session
loaded, _ := c.SkillLoad("agent-1", []string{"my-skill"})

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
      sessions/
        <session_id>/             # Session working directory
          skills -> ../../skills  # Symlink to agent's skills cache
```

- Default TTL: 24 hours
- Cleanup interval: 10 minutes
- Path traversal protection: paths containing `..` are rejected

## Pre-installed Environment

Based on Ubuntu 22.04, pre-installed with:

- **Python** 3.10 / 3.11 / 3.12 + scientific computing libraries (numpy, pandas, scipy, matplotlib, opencv, etc.)
- **Node.js** 22.x
- **System tools** — git, curl, wget, vim, jq, ripgrep, cmake, build-essential, etc.
- **uv** — High-speed Python package manager

## Audit Logging

All requests are logged to `/var/log/sandbox/audit.log`, including timestamp, request method/path/body, response status/body, latency, and client IP.
