package executor

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDirectExecutor_Prepare_BasicFields(t *testing.T) {
	d := &DirectExecutor{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := d.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: "/tmp",
		Env:        []string{"HOME=/tmp", "PATH=/usr/bin"},
	}, "echo", "hello")

	if cmd.Dir != "/tmp" {
		t.Errorf("expected Dir=/tmp, got %s", cmd.Dir)
	}
	if len(cmd.Env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(cmd.Env))
	}
	if cmd.Env[0] != "HOME=/tmp" {
		t.Errorf("expected HOME=/tmp, got %s", cmd.Env[0])
	}
	if cmd.Args[0] != "echo" {
		t.Errorf("expected args[0]=echo, got %s", cmd.Args[0])
	}
}

func TestDirectExecutor_Prepare_EmptyEnv(t *testing.T) {
	d := &DirectExecutor{}
	ctx := context.Background()

	cmd := d.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: "/tmp",
		Env:        nil,
	}, "ls")

	// nil Env means inherit parent env (default exec.Command behavior)
	if cmd.Env != nil {
		t.Errorf("expected nil Env, got %v", cmd.Env)
	}
}

func TestDirectExecutor_Prepare_Args(t *testing.T) {
	d := &DirectExecutor{}
	ctx := context.Background()

	cmd := d.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: "/tmp",
		Env:        os.Environ(),
	}, "bash", "-c", "echo hello")

	expectedArgs := []string{"bash", "-c", "echo hello"}
	if len(cmd.Args) != len(expectedArgs) {
		t.Fatalf("expected %d args, got %d: %v", len(expectedArgs), len(cmd.Args), cmd.Args)
	}
	for i, arg := range expectedArgs {
		if cmd.Args[i] != arg {
			t.Errorf("arg[%d]: expected %q, got %q", i, arg, cmd.Args[i])
		}
	}
}

func TestDirectExecutor_InitSession(t *testing.T) {
	d := &DirectExecutor{}
	dir := t.TempDir()
	sessionDir := dir + "/session"
	skillsDir := dir + "/skills"

	os.MkdirAll(sessionDir, 0755)
	os.MkdirAll(skillsDir, 0755)

	d.InitSession(sessionDir, skillsDir)

	// Verify skills symlink was created
	symlinkPath := sessionDir + "/skills"
	linkInfo, err := os.Lstat(symlinkPath)
	if err != nil {
		t.Fatalf("skills symlink not created: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Error("expected skills to be a symlink")
	}

	// Verify symlink target points to skillsDir
	target, err := os.Readlink(symlinkPath)
	if err != nil {
		t.Fatalf("failed to read symlink: %v", err)
	}
	if target != "../skills" {
		t.Errorf("expected symlink target '../skills', got %q", target)
	}
}

