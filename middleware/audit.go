package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/model"
	"github.com/gin-gonic/gin"
)

// sensitiveHeaders are redacted from audit logs.
var sensitiveHeaders = map[string]bool{
	"Authorization": true,
	"Cookie":        true,
	"Set-Cookie":    true,
	"X-Api-Key":     true,
}

type bodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w bodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// AuditLogger records full request/response for audit purposes,
// routing entries to per-session JSONL files via the audit writer.
func AuditLogger(w *audit.Writer) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Capture request body
		var reqBody interface{}
		rawBody, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(rawBody))
		json.Unmarshal(rawBody, &reqBody)

		// Extract agent_id and session_id from request body
		var agentID, sessionID string
		if m, ok := reqBody.(map[string]interface{}); ok {
			if v, ok := m["agent_id"].(string); ok {
				agentID = v
			}
			if v, ok := m["session_id"].(string); ok {
				sessionID = v
			}
		}

		// Fallback to query params for GET/DELETE requests
		if agentID == "" {
			agentID = c.Query("agent_id")
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
				canonical := strings.ToLower(k)
				redacted := false
				for sk := range sensitiveHeaders {
					if strings.ToLower(sk) == canonical {
						headers[k] = "[REDACTED]"
						redacted = true
						break
					}
				}
				if !redacted {
					headers[k] = v[0]
				}
			}
		}

		// Capture response
		bw := bodyWriter{body: bytes.NewBuffer(nil), ResponseWriter: c.Writer}
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
			w.WriteEntry(agentID, sessionID, entry)
		} else {
			w.WriteFallback(entry)
		}
	}
}
