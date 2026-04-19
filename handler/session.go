package handler

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/model"
	"github.com/hyponet/sandbox-container/session"

	"github.com/gin-gonic/gin"
)

// scannerBufPool reuses 1MB scanner buffers across GetAuditLogs requests.
var scannerBufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 1024*1024)
		return &buf
	},
}

type SessionHandler struct {
	mgr *session.Manager
}

func NewSessionHandler(mgr *session.Manager) *SessionHandler {
	return &SessionHandler{mgr: mgr}
}

// ListSessions returns all sessions for a given agent.
// GET /v1/sessions?agent_id=xxx
func (h *SessionHandler) ListSessions(c *gin.Context) {
	agentID := c.Query("agent_id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("agent_id is required"))
		return
	}
	if err := audit.ValidateID(agentID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid agent_id"))
		return
	}

	entries, err := h.mgr.ListSessions(agentID)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}

	sessions := make([]model.SessionInfo, 0, len(entries))
	for _, e := range entries {
		info := model.SessionInfo{
			SessionID: e.SessionID,
			AgentID:   agentID,
		}
		if !e.LastAccess.IsZero() {
			info.LastAccess = e.LastAccess.UTC().Format(time.RFC3339)
		}
		// Count audit log entries
		auditPath := h.mgr.AuditPath(agentID, e.SessionID)
		if count, err := countLines(auditPath); err == nil {
			info.AuditEntries = count
		}
		sessions = append(sessions, info)
	}

	c.JSON(http.StatusOK, model.OkResponse(model.SessionListResult{
		Sessions: sessions,
		Total:    len(sessions),
	}))
}

// GetAuditLogs returns paginated audit log entries for a session.
// GET /v1/sessions/:session_id/audits?agent_id=xxx&offset=0&limit=100
//
// Total reflects the number of valid (parseable) audit entries in the file,
// not the raw line count. Malformed lines are silently skipped.
func (h *SessionHandler) GetAuditLogs(c *gin.Context) {
	sessionID := c.Param("session_id")
	agentID := c.Query("agent_id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("agent_id is required"))
		return
	}
	if err := audit.ValidateID(agentID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid agent_id"))
		return
	}
	if err := audit.ValidateID(sessionID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid session_id"))
		return
	}

	offsetStr := c.DefaultQuery("offset", "0")
	limitStr := c.DefaultQuery("limit", "100")
	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid offset parameter"))
		return
	}
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 || limit > 1000 {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid limit parameter (must be 1-1000)"))
		return
	}

	// Sync the audit file before reading to ensure all buffered writes are visible.
	h.mgr.SyncAudit(agentID, sessionID)

	auditPath := h.mgr.AuditPath(agentID, sessionID)
	f, err := os.Open(auditPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, model.OkResponse(model.AuditLogResult{
				SessionID: sessionID,
				AgentID:   agentID,
				Entries:   []model.AuditEntry{},
				Total:     0,
				Offset:    offset,
				Limit:     limit,
			}))
			return
		}
		log.Printf("[ERROR] GetAuditLogs: read %s: %v", auditPath, err)
		c.JSON(http.StatusInternalServerError, model.ErrResponse("failed to read audit logs: "+err.Error()))
		return
	}
	defer f.Close()

	// Reuse scanner buffer from pool to reduce allocation pressure.
	bufPtr := scannerBufPool.Get().(*[]byte)
	defer scannerBufPool.Put(bufPtr)

	var entries []model.AuditEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(*bufPtr, len(*bufPtr))

	// Total counts only valid (parseable) entries, not raw lines.
	validCount := 0
	for scanner.Scan() {
		var entry model.AuditEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // skip malformed lines
		}
		validCount++
		if validCount <= offset {
			continue
		}
		if len(entries) < limit {
			entries = append(entries, entry)
		}
		// keep scanning to get accurate total
	}

	if entries == nil {
		entries = []model.AuditEntry{}
	}

	c.JSON(http.StatusOK, model.OkResponse(model.AuditLogResult{
		SessionID: sessionID,
		AgentID:   agentID,
		Entries:   entries,
		Total:     validCount,
		Offset:    offset,
		Limit:     limit,
	}))
}

// DeleteSession removes a session and its audit logs.
// DELETE /v1/sessions/:session_id?agent_id=xxx
func (h *SessionHandler) DeleteSession(c *gin.Context) {
	sessionID := c.Param("session_id")
	agentID := c.Query("agent_id")
	if agentID == "" {
		c.JSON(http.StatusBadRequest, model.ErrResponse("agent_id is required"))
		return
	}
	if err := audit.ValidateID(agentID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid agent_id"))
		return
	}
	if err := audit.ValidateID(sessionID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse("invalid session_id"))
		return
	}

	if !h.mgr.Exists(agentID, sessionID) {
		c.JSON(http.StatusNotFound, model.ErrResponse("session not found"))
		return
	}

	if err := h.mgr.DeleteSession(agentID, sessionID); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrResponse(err.Error()))
		return
	}
	c.JSON(http.StatusOK, model.OkMsg("session deleted"))
}

// countLines counts the number of lines in a file.
func countLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	return count, scanner.Err()
}
