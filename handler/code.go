package handler

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

type CodeHandler struct {
	mgr     *session.Manager
	exec    executor.CommandExecutor
	isBwrap bool
}

func NewCodeHandler(mgr *session.Manager, exec executor.CommandExecutor, isBwrap bool) *CodeHandler {
	return &CodeHandler{mgr: mgr, exec: exec, isBwrap: isBwrap}
}

func (h *CodeHandler) Execute(c *gin.Context) {
	var req model.CodeExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	roots := resolveRoots(h.mgr, req.AgentID, req.SessionID, req.EnableAgentWorkspace, req.UserID)
	workingDir := roots.HostRoot
	if req.Cwd != nil && *req.Cwd != "" {
		resolved, err := h.mgr.ResolvePathEx(req.AgentID, req.SessionID, *req.Cwd, req.EnableAgentWorkspace)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
			return
		}
		workingDir = resolved
	}
	if err := os.MkdirAll(workingDir, 0755); err != nil {
		log.Printf("[ERROR] Execute: mkdir %s: %v", workingDir, err)
	}

	mapping := sandboxPathMapping{HostRoot: roots.HostRoot, SkillsRoot: roots.SkillsRoot, UserdataRoot: roots.UserdataRoot}
	sandboxWorkingDir := hostToSandboxPath(h.isBwrap, mapping, workingDir)

	timeout := 30
	if req.Timeout != nil && *req.Timeout > 0 {
		timeout = *req.Timeout
	}
	if timeout > 300 {
		timeout = 300
	}

	var name string
	var args []string
	switch strings.ToLower(req.Language) {
	case "python":
		name, args = "python3", []string{"-c", req.Code}
	case "javascript", "js":
		name, args = "node", []string{"-e", req.Code}
	default:
		c.JSON(http.StatusBadRequest, model.ErrResponse("unsupported language: "+req.Language))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	rwBinds, roBinds := commandExecBinds(roots, req.EnableAgentWorkspace, h.isBwrap)
	cmd := h.exec.Prepare(executor.ExecOptions{
		Ctx:        ctx,
		WorkingDir: sandboxWorkingDir,
		Env:        buildIsolatedEnv(os.Environ(), sandboxWorkingDir, req.Env),
		RWBinds:    rwBinds,
		ROBinds:    roBinds,
	}, name, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	status := "completed"
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			status = "timed_out"
			exitCode = -1
		} else {
			exitCode = 1
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
		}
	}

	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	resp := model.CodeExecuteResponse{
		Language: req.Language,
		Status:   status,
		Code:     req.Code,
		Outputs:  []interface{}{},
	}
	if stdoutStr != "" {
		resp.Stdout = &stdoutStr
	}
	if stderrStr != "" {
		resp.Stderr = &stderrStr
	}
	if exitCode != 0 {
		resp.ExitCode = &exitCode
	}
	if err != nil && stderrStr != "" {
		resp.Traceback = strings.Split(strings.TrimSpace(stderrStr), "\n")
	}

	c.JSON(http.StatusOK, model.OkResponse(resp))
}

func (h *CodeHandler) Info(c *gin.Context) {
	pyVersion := getPythonVersion()
	nodeVersion := getNodeVersion()

	resp := model.CodeInfoResponse{
		Languages: []model.CodeLanguageInfo{
			{
				Language:       "python",
				Description:    fmt.Sprintf("Python %s runtime", pyVersion),
				RuntimeVersion: &pyVersion,
				DefaultTimeout: 30,
				MaxTimeout:     300,
				Details:        map[string]interface{}{"bin": "python3"},
			},
			{
				Language:       "javascript",
				Description:    fmt.Sprintf("Node.js %s runtime", nodeVersion),
				RuntimeVersion: &nodeVersion,
				DefaultTimeout: 30,
				MaxTimeout:     300,
				Details:        map[string]interface{}{"bin": "node"},
			},
		},
	}

	c.JSON(http.StatusOK, model.OkResponse(resp))
}
