package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceRoot(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	expected := filepath.Join(dir, "agent1", "workspace")
	if got := mgr.WorkspaceRoot("agent1"); got != expected {
		t.Errorf("WorkspaceRoot(\"agent1\") = %s, want %s", got, expected)
	}
}

func TestTouchWorkspace(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	// Verify sessionInit callback is invoked
	var calledSessionDir, calledSkillsDir string
	mgr.SetSessionInit(func(sessionDir, skillsDir string) {
		calledSessionDir = sessionDir
		calledSkillsDir = skillsDir
	})

	mgr.TouchWorkspace("agent1")

	// Verify the workspace directory was created.
	wsDir := filepath.Join(dir, "agent1", "workspace")
	info, err := os.Stat(wsDir)
	if err != nil {
		t.Fatalf("workspace directory not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected workspace to be a directory")
	}

	if calledSessionDir != wsDir {
		t.Errorf("expected sessionInit sessionDir=%s, got %s", wsDir, calledSessionDir)
	}
	expectedSkillsDir := filepath.Join(dir, "agent1", "skills")
	if calledSkillsDir != expectedSkillsDir {
		t.Errorf("expected sessionInit skillsDir=%s, got %s", expectedSkillsDir, calledSkillsDir)
	}
}

func TestResolvePathEx_WorkspaceMode(t *testing.T) {
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
		{
			name:      "non-skills path resolves under workspace",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/foo/bar.txt",
			want:      filepath.Join(dir, "a1", "workspace", "foo", "bar.txt"),
			wantErr:   false,
		},
		{
			name:      "root path resolves to workspace root",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/",
			want:      filepath.Join(dir, "a1", "workspace"),
			wantErr:   false,
		},
		{
			name:      "nested non-skills path",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/a/b/c/d.txt",
			want:      filepath.Join(dir, "a1", "workspace", "a", "b", "c", "d.txt"),
			wantErr:   false,
		},
		{
			name:      "skills path resolves to agent skills dir",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/skills/my/skill.txt",
			want:      filepath.Join(dir, "a1", "skills", "my", "skill.txt"),
			wantErr:   false,
		},
		{
			name:      "skills root resolves to agent skills dir",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/skills",
			want:      filepath.Join(dir, "a1", "skills"),
			wantErr:   false,
		},
		{
			name:      "skills path with trailing slash",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/skills/",
			want:      filepath.Join(dir, "a1", "skills"),
			wantErr:   false,
		},
		{
			name:      "path traversal rejected",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/../../../etc/passwd",
			want:      "",
			wantErr:   true,
		},
		{
			name:      "dotdot in middle rejected",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/foo/../../bar",
			want:      "",
			wantErr:   true,
		},
		{
			name:      "single dotdot rejected",
			agentID:   "a1",
			sessionID: "sess1",
			reqPath:   "/../secret",
			want:      "",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mgr.ResolvePathEx(tt.agentID, tt.sessionID, tt.reqPath, true)
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

func TestResolvePathEx_BackwardCompat(t *testing.T) {
	dir := t.TempDir()
	mgr := NewManager(dir, time.Hour)

	agentID := "a1"
	sessionID := "sess1"
	reqPaths := []string{
		"/foo/bar.txt",
		"/",
		"/deep/nested/path",
		"/skills/my/skill.txt",
		"/skills",
	}

	for _, reqPath := range reqPaths {
		// ResolvePathEx with disableSessionIsolation=false should produce
		// the same result as ResolvePath.
		classicPath, classicErr := mgr.ResolvePath(agentID, sessionID, reqPath)
		exPath, exErr := mgr.ResolvePathEx(agentID, sessionID, reqPath, false)

		if classicErr != nil && exErr == nil {
			t.Errorf("ResolvePath(%q) returned error but ResolvePathEx did not: classicErr=%v", reqPath, classicErr)
		}
		if classicErr == nil && exErr != nil {
			t.Errorf("ResolvePathEx(%q) returned error but ResolvePath did not: exErr=%v", reqPath, exErr)
		}
		if classicErr == nil && exPath != classicPath {
			t.Errorf("ResolvePathEx(%q, false) = %s, want %s (from ResolvePath)", reqPath, exPath, classicPath)
		}
	}

	// Also verify path traversal behavior is consistent.
	traversalPaths := []string{
		"/../../../etc/passwd",
		"/foo/../../bar",
	}
	for _, reqPath := range traversalPaths {
		_, classicErr := mgr.ResolvePath(agentID, sessionID, reqPath)
		_, exErr := mgr.ResolvePathEx(agentID, sessionID, reqPath, false)
		if classicErr == nil {
			t.Errorf("ResolvePath(%q) expected error, got nil", reqPath)
		}
		if exErr == nil {
			t.Errorf("ResolvePathEx(%q, false) expected error, got nil", reqPath)
		}
	}
}
