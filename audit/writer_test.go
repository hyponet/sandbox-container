package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestNewWriter(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	if w == nil {
		t.Fatal("writer is nil")
	}
	if w.root != dir {
		t.Errorf("expected root %s, got %s", dir, w.root)
	}
}

func TestWriteEntry(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	entry := map[string]string{"message": "hello", "agent": "a1"}
	err := w.WriteEntry("agent1", "session1", entry)
	if err != nil {
		t.Fatalf("WriteEntry failed: %v", err)
	}

	// Verify file was created
	path := filepath.Join(dir, "agent1", "audits", "session1.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("audit file not created: %v", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if !scanner.Scan() {
		t.Fatal("expected one line in audit file")
	}
	var result map[string]string
	if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse JSON: %v", err)
	}
	if result["message"] != "hello" {
		t.Errorf("expected message 'hello', got %q", result["message"])
	}
}

func TestWriteEntryMultipleEntries(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	for i := 0; i < 5; i++ {
		entry := map[string]interface{}{"index": i}
		if err := w.WriteEntry("a1", "s1", entry); err != nil {
			t.Fatalf("WriteEntry %d failed: %v", i, err)
		}
	}

	path := filepath.Join(dir, "a1", "audits", "s1.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("audit file not created: %v", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	if count != 5 {
		t.Errorf("expected 5 lines, got %d", count)
	}
}

func TestWriteEntryConcurrent(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := map[string]interface{}{"index": i}
			w.WriteEntry("a1", "s1", entry)
		}(i)
	}
	wg.Wait()

	path := filepath.Join(dir, "a1", "audits", "s1.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("audit file not created: %v", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var entry map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Errorf("invalid JSON line: %v", err)
		}
		count++
	}
	if count != 100 {
		t.Errorf("expected 100 lines, got %d", count)
	}
}

func TestWriteEntryMultipleSessions(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	w.WriteEntry("a1", "s1", map[string]string{"msg": "s1-entry"})
	w.WriteEntry("a1", "s2", map[string]string{"msg": "s2-entry"})
	w.WriteEntry("a2", "s1", map[string]string{"msg": "a2-s1-entry"})

	// Verify each file has exactly one entry
	for _, tc := range []struct {
		agent, session, expected string
	}{
		{"a1", "s1", "s1-entry"},
		{"a1", "s2", "s2-entry"},
		{"a2", "s1", "a2-s1-entry"},
	} {
		path := filepath.Join(dir, tc.agent, "audits", tc.session+".jsonl")
		f, err := os.Open(path)
		if err != nil {
			t.Errorf("audit file not created for %s/%s: %v", tc.agent, tc.session, err)
			continue
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		if !scanner.Scan() {
			t.Errorf("expected one line in %s/%s", tc.agent, tc.session)
			continue
		}
		var result map[string]string
		json.Unmarshal(scanner.Bytes(), &result)
		if result["msg"] != tc.expected {
			t.Errorf("expected msg %q, got %q", tc.expected, result["msg"])
		}
	}
}

func TestWriteFallback(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	entry := map[string]string{"msg": "fallback"}
	w.WriteFallback(entry)

	// Fallback writes to /var/log/sandbox/audit.log which we may not have access to
	// Just verify no panic
}

func TestCloseSession(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	w.WriteEntry("a1", "s1", map[string]string{"msg": "before-close"})
	w.CloseSession("a1", "s1")

	// Writing after close should re-open the file
	err := w.WriteEntry("a1", "s1", map[string]string{"msg": "after-close"})
	if err != nil {
		t.Fatalf("WriteEntry after CloseSession failed: %v", err)
	}

	path := filepath.Join(dir, "a1", "audits", "s1.jsonl")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("audit file not created: %v", err)
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 lines, got %d", count)
	}
}

func TestClose(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)

	w.WriteEntry("a1", "s1", map[string]string{"msg": "test"})
	w.Close()

	// Verify all handles are closed
	if len(w.handles) != 0 {
		t.Errorf("expected 0 handles after Close, got %d", len(w.handles))
	}
}

func TestAuditPath(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	expected := filepath.Join(dir, "myagent", "audits", "mysession.jsonl")
	got := w.auditPath("myagent", "mysession")
	if got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

// TestGetOrCreateReadOnlyDir verifies that getOrCreate returns an error
// when the audit directory cannot be created (e.g. read-only parent).
func TestGetOrCreateReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	w := NewWriter(dir, time.Minute)
	defer w.Close()

	// Write one entry to verify normal operation
	err := w.WriteEntry("a1", "s1", map[string]string{"msg": "ok"})
	if err != nil {
		t.Fatalf("initial write failed: %v", err)
	}

	// Create agent dir then make it read-only so MkdirAll for audits/ fails
	agentDir := filepath.Join(dir, "a2")
	os.MkdirAll(agentDir, 0755)
	os.Chmod(agentDir, 0555)
	defer os.Chmod(agentDir, 0755)

	err = w.WriteEntry("a2", "s1", map[string]string{"msg": "fail"})
	if err == nil {
		t.Error("expected error when audit dir is not writable")
	}
}

// TestNewWriterWithFallbackDir verifies fallback dir creation and usage.
func TestNewWriterWithFallbackDir(t *testing.T) {
	dir := t.TempDir()
	fallbackDir := filepath.Join(dir, "fallback")

	w := NewWriterWithFallback(dir, time.Minute, fallbackDir)
	defer w.Close()

	// Verify fallback dir was created
	info, err := os.Stat(fallbackDir)
	if err != nil {
		t.Fatalf("fallback dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("fallback should be a directory")
	}

	// Verify fallback audit.log was created
	if _, err := os.Stat(filepath.Join(fallbackDir, "audit.log")); err != nil {
		t.Fatalf("audit.log not created in fallback dir: %v", err)
	}
}
