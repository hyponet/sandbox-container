package handler

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

type CodeHandler struct {
	mgr *session.Manager
}

func NewCodeHandler(mgr *session.Manager) *CodeHandler {
	return &CodeHandler{mgr: mgr}
}

func (h *CodeHandler) Execute(c *gin.Context) {
	var req model.CodeExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	h.mgr.Touch(req.AgentID, req.SessionID)

	workingDir := h.mgr.SessionRoot(req.AgentID, req.SessionID)
	if req.Cwd != nil && *req.Cwd != "" {
		resolved, err := h.mgr.ResolvePath(req.AgentID, req.SessionID, *req.Cwd)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
			return
		}
		workingDir = resolved
	}
	os.MkdirAll(workingDir, 0755)

	timeout := 30
	if req.Timeout != nil && *req.Timeout > 0 {
		timeout = *req.Timeout
	}
	if timeout > 300 {
		timeout = 300
	}

	var cmd *exec.Cmd
	switch strings.ToLower(req.Language) {
	case "python":
		cmd = exec.Command("python3", "-c", req.Code)
	case "javascript", "js":
		cmd = exec.Command("node", "-e", req.Code)
	default:
		c.JSON(http.StatusBadRequest, model.ErrResponse("unsupported language: "+req.Language))
		return
	}

	cmd.Dir = workingDir

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	cmd = exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	cmd.Dir = workingDir

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
