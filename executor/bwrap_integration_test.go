//go:build integration

package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func skipIfNoBwrapIntegration(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not installed, skipping integration test")
	}
}

func TestBwrapIntegration_BasicExecution(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	dir := t.TempDir()
	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: dir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{dir},
		ROBinds:    nil,
	}, "bash", "-c", "echo hello")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "hello" {
		t.Errorf("expected 'hello', got %q", string(out))
	}
}

func TestBwrapIntegration_FilesystemIsolation(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	// Create a "secret" file outside the sandbox
	secretDir := t.TempDir()
	secretFile := filepath.Join(secretDir, "secret.txt")
	os.WriteFile(secretFile, []byte("top-secret-data"), 0644)

	sandboxDir := t.TempDir()
	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to read the secret file from inside the sandbox
	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: sandboxDir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{sandboxDir},
		ROBinds:    nil,
	}, "bash", "-c", "cat "+secretFile+" 2>&1 || echo FILE_NOT_ACCESSIBLE")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output: %s", err, out)
	}

	result := strings.TrimSpace(string(out))
	if strings.Contains(result, "top-secret-data") {
		t.Errorf("sandbox should NOT be able to read files outside its bind mounts, but it read: %s", result)
	}
}

func TestBwrapIntegration_WriteToSystemBlocked(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	dir := t.TempDir()
	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to write to a system path (should fail because /usr is read-only)
	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: dir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{dir},
		ROBinds:    nil,
	}, "bash", "-c", "echo test > /usr/bin/sandbox-write-test 2>&1; echo exit_code=$?")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output: %s", err, out)
	}

	result := strings.TrimSpace(string(out))
	if strings.Contains(result, "exit_code=0") {
		t.Fatalf("expected write to system path to fail, got %s", result)
	}

	// Verify /usr/bin/sandbox-write-test does NOT exist on host
	if _, err := os.Stat("/usr/bin/sandbox-write-test"); err == nil {
		os.Remove("/usr/bin/sandbox-write-test")
		t.Error("file should not have been created on the host system")
	}
}

func TestBwrapIntegration_PIDNamespace(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	dir := t.TempDir()
	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Inside the sandbox, the process should be PID 1 (or small number)
	// and should NOT see host processes
	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: dir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{dir},
		ROBinds:    nil,
	}, "bash", "-c", "echo my_pid=$(cat /proc/self/stat | awk '{print $1}')")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output: %s", err, out)
	}

	result := strings.TrimSpace(string(out))
	if !strings.HasPrefix(result, "my_pid=") {
		t.Errorf("unexpected output: %s", result)
	}
	// PID should be small (1 or a small number in the new namespace)
	pidStr := strings.TrimPrefix(result, "my_pid=")
	t.Logf("PID inside sandbox: %s", pidStr)
}

func TestBwrapIntegration_RWBindCanWrite(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	dir := t.TempDir()
	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: dir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{dir},
		ROBinds:    nil,
	}, "bash", "-c", "echo sandbox-written > test-file.txt && cat test-file.txt")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output: %s", err, out)
	}

	// Verify file exists on host
	data, err := os.ReadFile(filepath.Join(dir, "test-file.txt"))
	if err != nil {
		t.Fatalf("file not found on host: %v", err)
	}
	if strings.TrimSpace(string(data)) != "sandbox-written" {
		t.Errorf("expected 'sandbox-written', got %q", string(data))
	}
}

func TestBwrapIntegration_ROBindReadOnly(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	sandboxDir := t.TempDir()
	roDir := t.TempDir()
	os.WriteFile(filepath.Join(roDir, "data.txt"), []byte("read-only-content"), 0644)

	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Can read the RO file
	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: sandboxDir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{sandboxDir},
		ROBinds:    []string{roDir},
	}, "bash", "-c", "cat "+filepath.Join(roDir, "data.txt")+" && echo WRITE_TEST > "+filepath.Join(roDir, "data.txt")+" 2>&1 || true")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output: %s", err, out)
	}

	result := string(out)
	if !strings.Contains(result, "read-only-content") {
		t.Errorf("should be able to read from RO bind, got: %s", result)
	}

	// Verify the original file was NOT modified on host
	data, _ := os.ReadFile(filepath.Join(roDir, "data.txt"))
	if strings.Contains(string(data), "WRITE_TEST") {
		t.Error("RO bind should prevent writes to the mounted directory")
	}
}

func TestBwrapIntegration_SkillsSymlink(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	// Simulate the session + skills directory layout
	agentDir := t.TempDir()
	sessionDir := filepath.Join(agentDir, "sessions", "s1")
	skillsDir := filepath.Join(agentDir, "skills")
	os.MkdirAll(sessionDir, 0755)
	os.MkdirAll(filepath.Join(skillsDir, "my-skill"), 0755)
	os.WriteFile(filepath.Join(skillsDir, "my-skill", "hello.txt"), []byte("skill-content"), 0644)

	// Create symlink: sessions/s1/skills -> ../../skills
	os.Symlink("../../skills", filepath.Join(sessionDir, "skills"))

	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: sessionDir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{sessionDir},
		ROBinds:    []string{skillsDir},
	}, "bash", "-c", "cat skills/my-skill/hello.txt")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command failed: %v, output: %s", err, out)
	}
	if strings.TrimSpace(string(out)) != "skill-content" {
		t.Errorf("expected 'skill-content' via symlink, got %q", string(out))
	}
}

func TestBwrapIntegration_TimeoutKillsProcess(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	dir := t.TempDir()
	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: dir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{dir},
		ROBinds:    nil,
	}, "bash", "-c", "sleep 30")

	err = cmd.Run()
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestBwrapIntegration_StdinPassthrough(t *testing.T) {
	skipIfNoBwrapIntegration(t)

	dir := t.TempDir()
	e, err := NewBwrapExecutor(BwrapConfig{NetworkMode: "host"})
	if err != nil {
		t.Fatalf("NewBwrapExecutor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: dir,
		Env:        []string{"PATH=/usr/bin:/bin"},
		RWBinds:    []string{dir},
		ROBinds:    nil,
	}, "bash", "-c", "read line && echo got_$line")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	stdin.Write([]byte("hello_stdin\n"))
	stdin.Close()

	buf := make([]byte, 1024)
	n, _ := stdout.Read(buf)
	output := strings.TrimSpace(string(buf[:n]))

	cmd.Wait()

	if output != "got_hello_stdin" {
		t.Errorf("expected 'got_hello_stdin', got %q", output)
	}
}
