package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewManager(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)
	if mgr == nil {
		t.Fatal("manager is nil")
	}
	if mgr.root != dir {
		t.Errorf("expected root %s, got %s", dir, mgr.root)
	}
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	tests := []struct {
		name      string
		agentID   string
		sessionID string
		reqPath   string
		want      string
		wantErr   bool
	}{
		{"absolute path", "a1", "sess1", "/foo/bar.txt", filepath.Join(dir, "a1", "sessions", "sess1", "foo", "bar.txt"), false},
		{"root path", "a1", "sess1", "/", filepath.Join(dir, "a1", "sessions", "sess1"), false},
		{"nested path", "a1", "sess1", "/a/b/c", filepath.Join(dir, "a1", "sessions", "sess1", "a", "b", "c"), false},
		{"path traversal", "a1", "sess1", "/../../../etc/passwd", "", true},
		{"dotdot escape", "a1", "sess1", "/foo/../../bar", "", true},
		{"skills path", "a1", "sess1", "/skills/foo/bar.txt", filepath.Join(dir, "a1", "skills", "foo", "bar.txt"), false},
		{"skills root", "a1", "sess1", "/skills", filepath.Join(dir, "a1", "skills"), false},
		{"skills nested", "a1", "sess1", "/skills/a/b/c", filepath.Join(dir, "a1", "skills", "a", "b", "c"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mgr.ResolvePath(tt.agentID, tt.sessionID, tt.reqPath)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("expected %s, got %s", tt.want, got)
				}
			}
		})
	}
}

func TestEnsureDir(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	err := mgr.EnsureDir("a1", "sess1", "/deep/nested/dir")
	if err != nil {
		t.Fatalf("EnsureDir failed: %v", err)
	}

	expected := filepath.Join(dir, "a1", "sessions", "sess1", "deep", "nested", "dir")
	info, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestEnsureParentDir(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	err := mgr.EnsureParentDir("a1", "sess1", "/deep/nested/file.txt")
	if err != nil {
		t.Fatalf("EnsureParentDir failed: %v", err)
	}

	expected := filepath.Join(dir, "a1", "sessions", "sess1", "deep", "nested")
	info, err := os.Stat(expected)
	if err != nil {
		t.Fatalf("parent directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}
}

func TestTouch(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	// Verify sessionInit callback is invoked
	var calledSessionDir, calledSkillsDir string
	mgr.SetSessionInit(func(sessionDir, skillsDir string) {
		calledSessionDir = sessionDir
		calledSkillsDir = skillsDir
	})

	mgr.Touch("a1", "sess1")

	sessionDir := filepath.Join(dir, "a1", "sessions", "sess1")
	info, err := os.Stat(sessionDir)
	if err != nil {
		t.Fatalf("session directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected directory")
	}

	if calledSessionDir != sessionDir {
		t.Errorf("expected sessionInit sessionDir=%s, got %s", sessionDir, calledSessionDir)
	}
	expectedSkillsDir := filepath.Join(dir, "a1", "skills")
	if calledSkillsDir != expectedSkillsDir {
		t.Errorf("expected sessionInit skillsDir=%s, got %s", expectedSkillsDir, calledSkillsDir)
	}
}

func TestSessionIsolation(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	path1, _ := mgr.ResolvePath("a1", "sess1", "/file.txt")
	path2, _ := mgr.ResolvePath("a1", "sess2", "/file.txt")
	path3, _ := mgr.ResolvePath("a2", "sess1", "/file.txt")

	if path1 == path2 {
		t.Error("different sessions should resolve to different paths")
	}
	if path1 == path3 {
		t.Error("different agents should resolve to different paths")
	}
}

func TestSkillsRoot(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	expected := filepath.Join(dir, "a1", "skills")
	if got := mgr.SkillsRoot("a1"); got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestSessionRoot(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	expected := filepath.Join(dir, "a1", "sessions", "sess1")
	if got := mgr.SessionRoot("a1", "sess1"); got != expected {
		t.Errorf("expected %s, got %s", expected, got)
	}
}

func TestIsSkillsPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/skills", true},
		{"/skills/", true},
		{"/skills/foo", true},
		{"/skills/foo/bar.txt", true},
		{"/home/user", false},
		{"/", false},
		{"/skillsfile", false},
	}

	for _, tt := range tests {
		got := IsSkillsPath(tt.path)
		if got != tt.want {
			t.Errorf("IsSkillsPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestAgentRoot(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	expected := filepath.Join(dir, "a1")
	if got := mgr.AgentRoot("a1"); got != expected {
		t.Errorf("AgentRoot(a1) = %s, want %s", got, expected)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	// Session doesn't exist initially
	if mgr.Exists("a1", "sess1") {
		t.Error("expected session to not exist initially")
	}

	// Touch creates the session directory
	mgr.Touch("a1", "sess1")
	if !mgr.Exists("a1", "sess1") {
		t.Error("expected session to exist after Touch")
	}
}

func TestIsResolvedSkillsPath(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	skillsDir := filepath.Join(dir, "a1", "skills")
	sessionDir := filepath.Join(dir, "a1", "sessions", "s1")

	if !mgr.IsResolvedSkillsPath("a1", skillsDir) {
		t.Error("expected skills root to be a skills path")
	}
	if !mgr.IsResolvedSkillsPath("a1", filepath.Join(skillsDir, "foo", "bar.txt")) {
		t.Error("expected nested skills path to be a skills path")
	}
	if mgr.IsResolvedSkillsPath("a1", sessionDir) {
		t.Error("expected session path to not be a skills path")
	}
}
