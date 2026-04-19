package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ValidateID checks that an agent or session ID is safe for use in file paths.
// Rejects empty strings, strings containing "/" or "..", and path separators.
func ValidateID(id string) error {
	if id == "" {
		return fmt.Errorf("id must not be empty")
	}
	if strings.Contains(id, "/") || strings.Contains(id, "\\") || strings.Contains(id, "..") {
		return fmt.Errorf("id contains invalid characters: %q", id)
	}
	if id == "." {
		return fmt.Errorf("id must not be %q", id)
	}
	return nil
}

// Writer routes audit log entries to per-session JSONL files.
// File path: {root}/{agentID}/audits/{sessionID}.jsonl
type Writer struct {
	root     string
	mu       sync.Mutex
	handles  map[string]*fileHandle
	fallback *os.File
	maxIdle  time.Duration
	done     chan struct{}
}

type fileHandle struct {
	mu        sync.Mutex
	f         *os.File
	lastWrite time.Time
	closed    bool
}

// NewWriter creates a new audit writer.
// root is the agents base directory (e.g. "/data/agents").
// maxIdle controls how long idle file handles are kept open.
func NewWriter(root string, maxIdle time.Duration) *Writer {
	return NewWriterWithFallback(root, maxIdle, "")
}

// NewWriterWithFallback creates a new audit writer with a configurable fallback log directory.
// If fallbackDir is empty, it defaults to "/var/log/sandbox".
func NewWriterWithFallback(root string, maxIdle time.Duration, fallbackDir string) *Writer {
	if maxIdle == 0 {
		maxIdle = 5 * time.Minute
	}
	if fallbackDir == "" {
		fallbackDir = "/var/log/sandbox"
	}

	if err := os.MkdirAll(fallbackDir, 0755); err != nil {
		log.Printf("[ERROR] failed to create fallback audit dir %s: %v", fallbackDir, err)
	}
	fallback, err := os.OpenFile(filepath.Join(fallbackDir, "audit.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fallback = os.Stderr
	}

	w := &Writer{
		root:     root,
		handles:  make(map[string]*fileHandle),
		fallback: fallback,
		maxIdle:  maxIdle,
		done:     make(chan struct{}),
	}
	go w.evictLoop()
	return w
}

func handleKey(agentID, sessionID string) string {
	return agentID + "/" + sessionID
}

func (w *Writer) auditPath(agentID, sessionID string) string {
	return filepath.Join(w.root, agentID, "audits", sessionID+".jsonl")
}

// WriteEntry writes a JSON-marshaled audit entry to the per-session audit file.
func (w *Writer) WriteEntry(agentID, sessionID string, entry interface{}) error {
	if err := ValidateID(agentID); err != nil {
		return fmt.Errorf("invalid agent_id: %w", err)
	}
	if err := ValidateID(sessionID); err != nil {
		return fmt.Errorf("invalid session_id: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	return w.writeData(agentID, sessionID, data)
}

// writeData handles the actual file write with proper lock management.
func (w *Writer) writeData(agentID, sessionID string, data []byte) error {
	fh, err := w.getOrCreate(agentID, sessionID)
	if err != nil {
		return err
	}

	fh.mu.Lock()
	if fh.closed {
		// Handle was evicted — release this lock, purge stale entry, and retry once.
		fh.mu.Unlock()

		w.mu.Lock()
		key := handleKey(agentID, sessionID)
		if cur, ok := w.handles[key]; ok && cur == fh {
			delete(w.handles, key)
		}
		w.mu.Unlock()

		fh, err = w.getOrCreate(agentID, sessionID)
		if err != nil {
			return err
		}
		fh.mu.Lock()
	}
	defer fh.mu.Unlock()

	_, err = fh.f.Write(data)
	fh.lastWrite = time.Now()
	return err
}

// WriteFallback writes an entry to the global fallback audit log.
func (w *Writer) WriteFallback(entry interface{}) {
	data, _ := json.Marshal(entry)
	data = append(data, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fallback.Write(data)
}

func (w *Writer) getOrCreate(agentID, sessionID string) (*fileHandle, error) {
	key := handleKey(agentID, sessionID)

	w.mu.Lock()
	defer w.mu.Unlock()

	if fh, ok := w.handles[key]; ok && !fh.closed {
		return fh, nil
	}

	path := w.auditPath(agentID, sessionID)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create audit dir %s: %w", filepath.Dir(path), err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}

	fh := &fileHandle{f: f, lastWrite: time.Now()}
	w.handles[key] = fh
	return fh, nil
}

// CloseSession closes and removes the file handle for a session, syncing data to disk.
func (w *Writer) CloseSession(agentID, sessionID string) {
	key := handleKey(agentID, sessionID)

	w.mu.Lock()
	fh, ok := w.handles[key]
	if ok {
		delete(w.handles, key)
	}
	w.mu.Unlock()

	if ok {
		fh.mu.Lock()
		fh.closed = true
		fh.f.Sync()
		fh.f.Close()
		fh.mu.Unlock()
	}
}

// Close closes all file handles and stops the eviction loop.
func (w *Writer) Close() {
	close(w.done)

	w.mu.Lock()
	defer w.mu.Unlock()

	for key, fh := range w.handles {
		fh.f.Sync()
		fh.f.Close()
		fh.closed = true
		delete(w.handles, key)
	}
	if w.fallback != os.Stderr {
		w.fallback.Close()
	}
}

func (w *Writer) evictLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			w.evictIdle()
		}
	}
}

func (w *Writer) evictIdle() {
	w.mu.Lock()
	var toClose []*fileHandle
	var toDelete []string
	now := time.Now()
	for key, fh := range w.handles {
		if now.Sub(fh.lastWrite) > w.maxIdle {
			toClose = append(toClose, fh)
			toDelete = append(toDelete, key)
		}
	}
	for _, key := range toDelete {
		delete(w.handles, key)
	}
	w.mu.Unlock()

	for _, fh := range toClose {
		fh.mu.Lock()
		fh.closed = true
		fh.f.Sync()
		fh.f.Close()
		fh.mu.Unlock()
	}
}
