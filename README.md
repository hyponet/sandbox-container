# sandbox-container

一个基于 Go + Gin 构建的沙箱容器服务，提供隔离的命令执行、文件操作、代码运行和技能管理能力。

## 核心功能

- **Bash 执行** — 在隔离会话中执行 bash 命令，支持流式输出、异步模式、超时控制和进程交互（stdin 写入/kill）
- **文件操作** — 完整的文件管理：读写、搜索、glob/grep、目录列表、文件上传下载、字符串替换
- **代码执行** — 运行 Python 和 JavaScript 代码，支持超时控制，预装丰富的科学计算和 Web 开发库
- **Skills 管理** — 从 ZIP 归档下载和加载技能包
- **会话隔离** — 基于 `agent_id` + `session_id` 的目录隔离，TTL 自动过期清理，防止路径穿越
- **审计日志** — 全量请求/响应日志记录

## 快速开始

### Docker 运行

```bash
docker build -t sandbox-container .

docker run -d \
  -p 9090:9090 \
  -v sandbox-data:/data/agents \
  -v sandbox-logs:/var/log/sandbox \
  sandbox-container
```

服务启动后监听 `9090` 端口，健康检查端点为 `GET /v1/sandbox`。

### 本地开发

```bash
go run .
```

## API 概览

### 沙箱信息

```
GET  /v1/sandbox                # 获取沙箱环境信息（OS、运行时、工具）
GET  /v1/sandbox/packages/python # 列出已安装 Python 包
GET  /v1/sandbox/packages/nodejs # 列出已安装 Node.js 包
```

### Bash 执行

```
POST /v1/bash/exec              # 执行命令
POST /v1/bash/output            # 读取增量输出（流式）
POST /v1/bash/write             # 写入 stdin
POST /v1/bash/kill              # 终止命令
GET  /v1/bash/sessions          # 列出 bash 会话
POST /v1/bash/sessions/create   # 创建持久 bash 会话
POST /v1/bash/sessions/:id/close # 关闭 bash 会话
```

**执行命令示例：**

```json
POST /v1/bash/exec
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "command": "echo hello",
  "timeout": 30
}
```

### 文件操作

```
POST /v1/file/read     # 读取文件
POST /v1/file/write    # 写入文件
POST /v1/file/replace  # 字符串替换
POST /v1/file/search   # 正则搜索文件内容
POST /v1/file/find     # 按 glob 模式查找文件
POST /v1/file/grep     # 跨文件 grep
POST /v1/file/glob     # glob 匹配
POST /v1/file/list     # 列出目录内容
POST /v1/file/upload   # 上传文件
GET  /v1/file/download # 下载文件
```

**读写文件示例：**

```json
POST /v1/file/write
{
  "agent_id": "agent-1",
  "session_id": "session-1",
  "file": "test.txt",
  "content": "hello world"
}
```

### 代码执行

```
POST /v1/code/execute  # 执行代码（Python / JavaScript）
GET  /v1/code/info     # 获取支持的运行时信息
```

**执行代码示例：**

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

### Skills 管理

```
POST /v1/skills/list  # 下载并列出技能
POST /v1/skills/load  # 加载技能内容
```

## Go 客户端

```go
import "sandbox-container/client"

c := client.NewClient("http://localhost:9090")

// 执行 bash 命令
result, _ := c.BashExec("agent-1", "session-1", "ls -la",
    client.WithTimeout(30),
    client.WithEnv(map[string]string{"FOO": "bar"}))

// 执行代码
result, _ := c.CodeExecute("agent-1", "session-1", "python",
    "print('hello')", client.WithCodeTimeout(30))

// 文件操作
content, _ := c.FileRead("agent-1", "session-1", "/workspace/main.go",
    client.WithLineRange(0, 100))
c.FileWrite("agent-1", "session-1", "test.txt", "hello")
files, _ := c.FileGlob("agent-1", "session-1", "/", "**/*.go")

// Skills
skills, _ := c.SkillList("agent-1", []string{"https://example.com/skill.zip"})
content, _ := c.SkillLoad("agent-1", []string{"my-skill"})
```

## 会话与隔离

每个 `agent_id` + `session_id` 对应独立的文件目录：

```
/data/agents/
  <agent_id>/
    skills/                    # 共享技能目录（只读）
    sessions/
      <session_id>/            # 会话工作目录
        skills -> ../../skills # 软链接到共享技能
```

- 默认 TTL：24 小时
- 清理间隔：10 分钟
- 路径穿越防护：拒绝包含 `..` 的路径

## 预装环境

基于 Ubuntu 22.04，预装：

- **Python** 3.10 / 3.11 / 3.12 + 科学计算库（numpy、pandas、scipy、matplotlib、opencv 等）
- **Node.js** 22.x
- **系统工具** — git、curl、wget、vim、jq、ripgrep、cmake、build-essential 等
- **uv** — 高速 Python 包管理器

## 审计日志

所有请求记录到 `/var/log/sandbox/audit.log`，包含时间戳、请求方法/路径/请求体、响应状态码/响应体、延迟和客户端 IP。
