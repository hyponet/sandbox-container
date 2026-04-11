package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
)

var auditLogger *log.Logger

func init() {
	logDir := "/var/log/sandbox"
	os.MkdirAll(logDir, 0755)
	f, err := os.OpenFile(filepath.Join(logDir, "audit.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// Fallback to stderr if log file can't be opened
		auditLogger = log.New(os.Stderr, "[AUDIT] ", log.LstdFlags)
		return
	}
	auditLogger = log.New(f, "", 0) // we handle our own timestamps
}

// AuditEntry represents a single audit log record.
type AuditEntry struct {
	Timestamp   string            `json:"timestamp"`
	Method      string            `json:"method"`
	Path        string            `json:"path"`
	Headers     map[string]string `json:"headers"`
	RequestBody interface{}       `json:"request_body,omitempty"`
	Status      int               `json:"status"`
	Response    interface{}       `json:"response,omitempty"`
	Latency     string            `json:"latency"`
	ClientIP    string            `json:"client_ip"`
}

type bodyWriter struct {
	gin.ResponseWriter
	body *bytes.Buffer
}

func (w bodyWriter) Write(b []byte) (int, error) {
	w.body.Write(b)
	return w.ResponseWriter.Write(b)
}

// AuditLogger records full request/response for audit purposes.
func AuditLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Capture request body
		var reqBody interface{}
		rawBody, _ := io.ReadAll(c.Request.Body)
		c.Request.Body = io.NopCloser(bytes.NewBuffer(rawBody))
		json.Unmarshal(rawBody, &reqBody)

		// Capture headers
		headers := make(map[string]string)
		for k, v := range c.Request.Header {
			if len(v) > 0 {
				headers[k] = v[0]
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

		entry := AuditEntry{
			Timestamp:   start.UTC().Format(time.RFC3339Nano),
			Method:      c.Request.Method,
			Path:        c.Request.URL.Path,
			Headers:     headers,
			RequestBody: reqBody,
			Status:      c.Writer.Status(),
			Response:    respBody,
			Latency:     latency.String(),
			ClientIP:    c.ClientIP(),
		}

		data, _ := json.Marshal(entry)
		auditLogger.Println(string(data))
	}
}
