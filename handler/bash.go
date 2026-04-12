package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type BashHandler struct {
	mgr      *session.Manager
	sessions map[string]*bashSession // key: "sandboxSID:bashSID"
	mu       sync.RWMutex
}

type bashSession struct {
	sandboxSID  string
	bashSID     string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdoutBuf   *threadSafeBuffer
	stderrBuf   *threadSafeBuffer
	workingDir  string
	createdAt   time.Time
	lastUsedAt  time.Time
	status     model.CommandStatus
	exitCode    *int
	commandID   string
	command     string
	cancel      context.CancelFunc
	commandCount int
}

type threadSafeBuffer struct {
	mu   sync.RWMutex
	buf  bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) ReadFromOffset(offset int) string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	data := b.buf.Bytes()
	if offset >= len(data) {
		return ""
	}
	return string(data[offset:])
}

func (b *threadSafeBuffer) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.buf.Len()
}

func NewBashHandler(mgr *session.Manager) *BashHandler {
	return &BashHandler{
		mgr:      mgr,
		sessions: make(map[string]*bashSession),
	}
}

func (h *BashHandler) sessionKey(sandboxSID, bashSID string) string {
	return sandboxSID + ":" + bashSID
}

func (h *BashHandler) CreateSession(c *gin.Context) {
	var req model.BashSessionCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	bashSID := "default"
	if req.BashSID != nil && *req.BashSID != "" {
		bashSID = *req.BashSID
	}

	key := h.sessionKey(req.SessionID, bashSID)
	h.mu.RLock()
	_, exists := h.sessions[key]
	h.mu.RUnlock()

	if exists {
		c.JSON(http.StatusOK, model.OkResponse(model.BashSessionInfo{
			SessionID:  bashSID,
			Status:     model.SessionReady,
			WorkingDir: h.sessions[key].workingDir,
			CreatedAt:  h.sessions[key].createdAt,
			LastUsedAt: h.sessions[key].lastUsedAt,
		}))
		return
	}

	// Determine working dir within session
	workingDir := h.mgr.SessionRoot(req.AgentID, req.SessionID)
	if req.ExecDir != nil && *req.ExecDir != "" {
		resolved, err := h.mgr.ResolvePath(req.AgentID, req.SessionID, *req.ExecDir)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
			return
		}
		workingDir = resolved
	}
	os.MkdirAll(workingDir, 0755)

	bs := &bashSession{
		sandboxSID: req.SessionID,
		bashSID:    bashSID,
		workingDir: workingDir,
		createdAt:  time.Now(),
		lastUsedAt: time.Now(),
		status:     model.CommandStatus(model.SessionReady),
	}

	h.mu.Lock()
	h.sessions[key] = bs
	h.mu.Unlock()

	c.JSON(http.StatusOK, model.OkResponse(model.BashSessionInfo{
		SessionID:  bashSID,
		Status:     model.SessionReady,
		WorkingDir: workingDir,
		CreatedAt:  bs.createdAt,
		LastUsedAt: bs.lastUsedAt,
	}))
}

func (h *BashHandler) Exec(c *gin.Context) {
	var req model.BashExecRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	h.mgr.Touch(req.AgentID, req.SessionID)
	bashSID := "default"
	key := h.sessionKey(req.SessionID, bashSID)

	// Determine working dir
	workingDir := h.mgr.SessionRoot(req.AgentID, req.SessionID)
	if req.ExecDir != nil && *req.ExecDir != "" {
		resolved, err := h.mgr.ResolvePath(req.AgentID, req.SessionID, *req.ExecDir)
		if err != nil {
			c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
			return
		}
		workingDir = resolved
	}
	os.MkdirAll(workingDir, 0755)

	cmdID := uuid.New().String()[:8]
	timeout := 30.0
	if req.Timeout != nil {
		timeout = *req.Timeout
	}

	hardTimeout := 0.0
	if req.HardTimeout != nil {
		hardTimeout = *req.HardTimeout
	}

	// Build command
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	cmd := exec.CommandContext(ctx, "bash", "-c", req.Command)
	cmd.Dir = workingDir

	// Set environment
	cmd.Env = os.Environ()
	for k, v := range req.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdoutBuf := &threadSafeBuffer{}
	stderrBuf := &threadSafeBuffer{}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to create stdin pipe: "+err.Error()))
		return
	}

	if err := cmd.Start(); err != nil {
		cancel()
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to start command: "+err.Error()))
		return
	}

	bs := &bashSession{
		sandboxSID:  req.SessionID,
		bashSID:     bashSID,
		cmd:         cmd,
		stdin:       stdinPipe,
		stdoutBuf:   stdoutBuf,
		stderrBuf:   stderrBuf,
		workingDir:  workingDir,
		createdAt:   time.Now(),
		lastUsedAt:  time.Now(),
		status:      model.StatusRunning,
		commandID:   cmdID,
		command:     req.Command,
		cancel:      cancel,
	}

	h.mu.Lock()
	h.sessions[key] = bs
	h.mu.Unlock()

	if req.AsyncMode {
		go h.waitCommand(key, cmd, hardTimeout)
		c.JSON(http.StatusOK, model.OkResponse(model.BashExecResult{
			SessionID: bashSID,
			CommandID: cmdID,
			Command:   req.Command,
			Status:    model.StatusRunning,
		}))
		return
	}

	// Sync mode: wait for completion
	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
		exitCode := cmd.ProcessState.ExitCode()
		stdout := stdoutBuf.ReadFromOffset(0)
		stderr := stderrBuf.ReadFromOffset(0)

		bs.status = model.StatusCompleted
		bs.exitCode = &exitCode

		result := model.BashExecResult{
			SessionID:    bashSID,
			CommandID:    cmdID,
			Command:      req.Command,
			Status:       model.StatusCompleted,
			Stdout:       &stdout,
			Stderr:       &stderr,
			ExitCode:     &exitCode,
			Offset:       stdoutBuf.Len(),
			StderrOffset: stderrBuf.Len(),
		}
		c.JSON(http.StatusOK, model.OkResponse(result))

	case <-ctx.Done():
		// Timeout
		cmd.Process.Kill()
		stdout := stdoutBuf.ReadFromOffset(0)
		stderr := stderrBuf.ReadFromOffset(0)
		exitCode := -1

		bs.status = model.StatusTimedOut
		bs.exitCode = &exitCode

		result := model.BashExecResult{
			SessionID:    bashSID,
			CommandID:    cmdID,
			Command:      req.Command,
			Status:       model.StatusTimedOut,
			Stdout:       &stdout,
			Stderr:       &stderr,
			ExitCode:     &exitCode,
			Offset:       stdoutBuf.Len(),
			StderrOffset: stderrBuf.Len(),
		}
		c.JSON(http.StatusOK, model.OkResponse(result))
	}
}

func (h *BashHandler) waitCommand(key string, cmd *exec.Cmd, hardTimeout float64) {
	if hardTimeout > 0 {
		timer := time.AfterFunc(time.Duration(hardTimeout*float64(time.Second)), func() {
			cmd.Process.Kill()
		})
		defer timer.Stop()
	}

	cmd.Wait()

	h.mu.Lock()
	defer h.mu.Unlock()
	if bs, ok := h.sessions[key]; ok {
		exitCode := cmd.ProcessState.ExitCode()
		bs.status = model.StatusCompleted
		bs.exitCode = &exitCode
		bs.commandCount++
	}
}

func (h *BashHandler) Output(c *gin.Context) {
	var req model.BashOutputRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	key := h.sessionKey(req.SessionID, "default")
	h.mu.RLock()
	bs, ok := h.sessions[key]
	h.mu.RUnlock()

	if !ok {
		c.JSON(http.StatusNotFound, model.ErrResponse("bash session not found"))
		return
	}

	stdout := bs.stdoutBuf.ReadFromOffset(req.Offset)
	stderr := bs.stderrBuf.ReadFromOffset(req.StderrOffset)

	result := model.BashOutputResult{
		SessionID:    req.SessionID,
		Stdout:       stdout,
		Stderr:       stderr,
		Offset:       bs.stdoutBuf.Len(),
		StderrOffset: bs.stderrBuf.Len(),
		Command: &model.BashCommandInfo{
			CommandID: bs.commandID,
			Command:   bs.command,
			Status:    bs.status,
			ExitCode:  bs.exitCode,
		},
	}
	c.JSON(http.StatusOK, model.OkResponse(result))
}

func (h *BashHandler) Write(c *gin.Context) {
	var req model.BashWriteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	key := h.sessionKey(req.SessionID, "default")
	h.mu.RLock()
	bs, ok := h.sessions[key]
	h.mu.RUnlock()

	if !ok || bs.stdin == nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("bash session not found or no active process"))
		return
	}

	bs.stdin.Write([]byte(req.Input))
	c.JSON(http.StatusOK, model.OkMsg("written"))
}

func (h *BashHandler) Kill(c *gin.Context) {
	var req model.BashKillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid request: "+err.Error()))
		return
	}

	key := h.sessionKey(req.SessionID, "default")
	h.mu.RLock()
	bs, ok := h.sessions[key]
	h.mu.RUnlock()

	if !ok || bs.cmd == nil || bs.cmd.Process == nil {
		c.JSON(http.StatusNotFound, model.ErrResponse("bash session not found"))
		return
	}

	sig := req.Signal
	if sig == "" {
		sig = "SIGTERM"
	}

	switch sig {
	case "SIGKILL":
		bs.cmd.Process.Kill()
	case "SIGINT":
		bs.cmd.Process.Signal(os.Interrupt)
	default:
		bs.cmd.Process.Signal(os.Interrupt)
	}

	exitCode := -1
	bs.status = model.CommandStatus(model.StatusKilled)
	bs.exitCode = &exitCode

	c.JSON(http.StatusOK, model.OkResponse(gin.H{
		"status":    model.StatusKilled,
		"exit_code": exitCode,
	}))
}

func (h *BashHandler) ListSessions(c *gin.Context) {
	sessionID := c.Query("session_id")
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("session_id is required"))
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	var sessions []model.BashSessionInfo
	for key, bs := range h.sessions {
		parts := strings.SplitN(key, ":", 2)
		if len(parts) == 2 && parts[0] == sessionID {
			sessions = append(sessions, model.BashSessionInfo{
				SessionID:      bs.bashSID,
				Status:         model.SessionReady,
				WorkingDir:     bs.workingDir,
				CreatedAt:      bs.createdAt,
				LastUsedAt:     bs.lastUsedAt,
				CurrentCommand: &bs.command,
				CommandCount:   bs.commandCount,
			})
		}
	}

	c.JSON(http.StatusOK, model.OkResponse(sessions))
}

func (h *BashHandler) CloseSession(c *gin.Context) {
	bashSID := c.Param("session_id")
	var req model.BashSessionCloseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// session_id from path, sandbox session_id from query
		req.SessionID = c.Query("sandbox_session_id")
		if req.SessionID == "" {
			c.JSON(http.StatusBadRequest, model.ErrResponse("session_id is required"))
			return
		}
	}

	key := h.sessionKey(req.SessionID, bashSID)
	h.mu.Lock()
	defer h.mu.Unlock()

	bs, ok := h.sessions[key]
	if !ok {
		c.JSON(http.StatusNotFound, model.ErrResponse("bash session not found"))
		return
	}

	if bs.cancel != nil {
		bs.cancel()
	}
	if bs.cmd != nil && bs.cmd.Process != nil {
		bs.cmd.Process.Kill()
	}
	delete(h.sessions, key)

	c.JSON(http.StatusOK, model.OkMsg("session closed"))
}
