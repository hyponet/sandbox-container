package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/model"
)

// sensitiveHeaders are redacted from audit logs (keys stored in lowercase).
var sensitiveHeaders = map[string]bool{
	"authorization": true,
	"cookie":        true,
	"set-cookie":    true,
	"x-api-key":     true,
}

// sensitiveRequestFields are redacted from audit request bodies.
var sensitiveRequestFields = map[string]bool{
	"env": true,
}

const redactedValue = "[REDACTED]"

// maxAuditBodySize limits how much of the request body is stored in the audit record.
const maxAuditBodySize = 512 * 1024 // 512KB

// maxAuditRespSize limits how much of the response body is stored in the audit record.
const maxAuditRespSize = 512 * 1024 // 512KB

// maxIDExtractSize is the maximum number of bytes read to extract agent_id/session_id.
// This is kept small so that even if the full body is huge, ID extraction is cheap.
const maxIDExtractSize = 4 * 1024 // 4KB

type bodyWriter struct {
	gin.ResponseWriter
	body    *bytes.Buffer
	capped  bool
	maxSize int
}

func (w *bodyWriter) Write(b []byte) (int, error) {
	if !w.capped {
		remaining := w.maxSize - w.body.Len()
		if remaining > 0 {
			if len(b) <= remaining {
				w.body.Write(b)
			} else {
				w.body.Write(b[:remaining])
				w.capped = true
			}
		} else {
			w.capped = true
		}
	}
	return w.ResponseWriter.Write(b)
}

func (w *bodyWriter) WriteString(s string) (int, error) {
	if !w.capped {
		remaining := w.maxSize - w.body.Len()
		if remaining > 0 {
			if len(s) <= remaining {
				w.body.WriteString(s)
			} else {
				w.body.WriteString(s[:remaining])
				w.capped = true
			}
		} else {
			w.capped = true
		}
	}
	return io.WriteString(w.ResponseWriter, s)
}

// idPattern matches "agent_id":"value" or "session_id":"value" in JSON,
// tolerating truncated bodies where json.Unmarshal would fail.
var idPattern = regexp.MustCompile(`"(agent_id|session_id)"\s*:\s*"([^"]*)"`)

// extractIDs pulls agent_id and session_id from a JSON byte slice.
// Uses regexp so it works on truncated JSON where Unmarshal would fail.
func extractIDs(data []byte) (agentID, sessionID string) {
	for _, match := range idPattern.FindAllSubmatch(data, -1) {
		key := string(match[1])
		val := string(match[2])
		switch key {
		case "agent_id":
			if agentID == "" {
				agentID = val
			}
		case "session_id":
			if sessionID == "" {
				sessionID = val
			}
		}
		if agentID != "" && sessionID != "" {
			break
		}
	}
	return
}

func redactSensitiveRequestBody(v interface{}) interface{} {
	switch body := v.(type) {
	case map[string]interface{}:
		redacted := make(map[string]interface{}, len(body))
		for k, child := range body {
			if sensitiveRequestFields[strings.ToLower(k)] {
				redacted[k] = redactSensitiveValue(child)
				continue
			}
			redacted[k] = redactSensitiveRequestBody(child)
		}
		return redacted
	case []interface{}:
		redacted := make([]interface{}, len(body))
		for i, child := range body {
			redacted[i] = redactSensitiveRequestBody(child)
		}
		return redacted
	default:
		return v
	}
}

func redactSensitiveValue(v interface{}) interface{} {
	switch value := v.(type) {
	case map[string]interface{}:
		redacted := make(map[string]interface{}, len(value))
		for k := range value {
			redacted[k] = redactedValue
		}
		return redacted
	case []interface{}:
		redacted := make([]interface{}, len(value))
		for i := range value {
			redacted[i] = redactedValue
		}
		return redacted
	default:
		return redactedValue
	}
}

// AuditLogger records full request/response for audit purposes,
// routing entries to per-session JSONL files via the audit writer.
func AuditLogger(w *audit.Writer) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Read the full request body so the handler can consume it normally.
		// We use io.LimitReader to cap what we keep for the audit record,
		// but we must restore the full body for the handler.
		fullBody, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewReader(fullBody))

		// Extract agent_id/session_id from the first few KB of the body.
		// This works even when the full body is very large (e.g. file uploads)
		// because we only parse a small prefix for ID extraction.
		idSlice := fullBody
		if len(idSlice) > maxIDExtractSize {
			idSlice = idSlice[:maxIDExtractSize]
		}
		agentID, sessionID := extractIDs(idSlice)

		// Build the audit request body from the (possibly truncated) body.
		var reqBody interface{}
		auditBody := fullBody
		if len(auditBody) > maxAuditBodySize {
			auditBody = auditBody[:maxAuditBodySize]
		}
		json.Unmarshal(auditBody, &reqBody)
		reqBody = redactSensitiveRequestBody(reqBody)

		// Fallback to multipart form fields (e.g. file upload)
		if agentID == "" {
			agentID = c.PostForm("agent_id")
		}
		if sessionID == "" {
			sessionID = c.PostForm("session_id")
		}

		// Fallback to path params and query params
		if agentID == "" {
			agentID = c.Param("agent_id")
			if agentID == "" {
				agentID = c.Query("agent_id")
			}
		}
		if sessionID == "" {
			sessionID = c.Param("session_id")
			if sessionID == "" {
				sessionID = c.Query("session_id")
			}
		}

		// Capture headers, redacting sensitive ones
		headers := make(map[string]string)
		for k, v := range c.Request.Header {
			if len(v) > 0 {
				if sensitiveHeaders[strings.ToLower(k)] {
					headers[k] = redactedValue
				} else {
					headers[k] = v[0]
				}
			}
		}

		// Capture response with a capped buffer
		bw := &bodyWriter{
			body:           bytes.NewBuffer(nil),
			ResponseWriter: c.Writer,
			maxSize:        maxAuditRespSize,
		}
		c.Writer = bw

		start := time.Now()
		c.Next()
		latency := time.Since(start)

		// Parse response body
		var respBody interface{}
		json.Unmarshal(bw.body.Bytes(), &respBody)

		entry := model.AuditEntry{
			Timestamp:   start.UTC().Format(time.RFC3339Nano),
			AgentID:     agentID,
			SessionID:   sessionID,
			Method:      c.Request.Method,
			Path:        c.Request.URL.Path,
			Headers:     headers,
			RequestBody: reqBody,
			Status:      c.Writer.Status(),
			Response:    respBody,
			Latency:     latency.String(),
			ClientIP:    c.ClientIP(),
		}

		if agentID != "" && sessionID != "" {
			if err := w.WriteEntry(agentID, sessionID, entry); err != nil {
				log.Printf("[AUDIT] failed to write entry for agent=%s session=%s path=%s: %v", agentID, sessionID, c.Request.URL.Path, err)
			}
		} else {
			log.Printf("[AUDIT] missing identity: agent_id=%q session_id=%q method=%s path=%s client=%s, writing to fallback log", agentID, sessionID, c.Request.Method, c.Request.URL.Path, c.ClientIP())
			w.WriteFallback(entry)
		}
	}
}
