package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBwrapExecutor_buildArgs_HostMode(t *testing.T) {
	e := &BwrapExecutor{cfg: BwrapConfig{NetworkMode: "host"}, path: "/usr/bin/bwrap"}

	args := e.buildArgs(ExecOptions{
		Ctx:        context.Background(),
		WorkingDir: "/data/agents/a1/sessions/s1",
		Env:        os.Environ(),
		RWBinds:    []string{"/data/agents/a1/sessions/s1"},
		ROBinds:    []string{"/data/agents/a1/skills"},
	}, nil)

	argsStr := strings.Join(args, " ")

	// Must contain core namespace flags
	for _, flag := range []string{"--die-with-parent", "--new-session", "--unshare-pid", "--unshare-uts", "--unshare-ipc"} {
		if !strings.Contains(argsStr, flag) {
			t.Errorf("expected args to contain %s, got: %s", flag, argsStr)
		}
	}

	// Must NOT contain network isolation in host mode
	if strings.Contains(argsStr, "--unshare-net") {
		t.Error("expected --unshare-net to be absent in host mode")
	}

	// Must contain RW bind for session dir
	if !strings.Contains(argsStr, "--bind /data/agents/a1/sessions/s1 /data/agents/a1/sessions/s1") {
		t.Errorf("expected RW bind for session dir, got: %s", argsStr)
	}

	// Must contain RO bind for skills dir
	if !strings.Contains(argsStr, "--ro-bind /data/agents/a1/skills /data/agents/a1/skills") {
		t.Errorf("expected RO bind for skills dir, got: %s", argsStr)
	}

	// Must contain tmpfs for /tmp
	if !strings.Contains(argsStr, "--tmpfs /tmp") {
		t.Errorf("expected tmpfs /tmp, got: %s", argsStr)
	}

	// Must contain chdir
	if !strings.Contains(argsStr, "--chdir /data/agents/a1/sessions/s1") {
		t.Errorf("expected --chdir, got: %s", argsStr)
	}
}

func TestBwrapExecutor_buildArgs_IsolatedMode(t *testing.T) {
	e := &BwrapExecutor{cfg: BwrapConfig{NetworkMode: "isolated"}, path: "/usr/bin/bwrap"}

	args := e.buildArgs(ExecOptions{
		Ctx:        context.Background(),
		WorkingDir: "/tmp/test",
		Env:        os.Environ(),
		RWBinds:    []string{"/tmp/test"},
		ROBinds:    nil,
	}, nil)

	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "--unshare-net") {
		t.Error("expected --unshare-net in isolated mode")
	}
}

func TestBwrapExecutor_buildArgs_ExtraROBinds(t *testing.T) {
	e := &BwrapExecutor{cfg: BwrapConfig{
		NetworkMode:  "host",
		ExtraROBinds: []string{"/opt/tools", "/nonexistent/path"},
	}, path: "/usr/bin/bwrap"}

	args := e.buildArgs(ExecOptions{
		Ctx:        context.Background(),
		WorkingDir: "/tmp/test",
		Env:        os.Environ(),
		RWBinds:    []string{"/tmp/test"},
		ROBinds:    nil,
	}, nil)

	argsStr := strings.Join(args, " ")

	// /opt/tools only included if it exists on this system
	if _, err := os.Stat("/opt/tools"); err == nil {
		if !strings.Contains(argsStr, "--ro-bind /opt/tools /opt/tools") {
			t.Error("expected extra RO bind for /opt/tools")
		}
	}

	// /nonexistent/path should be silently skipped
	if strings.Contains(argsStr, "/nonexistent/path") {
		t.Error("nonexistent path should be skipped")
	}
}

func TestBwrapExecutor_Prepare_CmdFields(t *testing.T) {
	e := &BwrapExecutor{cfg: BwrapConfig{}, path: "/usr/bin/bwrap"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := e.Prepare(ExecOptions{
		Ctx:        ctx,
		WorkingDir: "/tmp/test",
		Env:        []string{"HOME=/tmp/test"},
		RWBinds:    []string{"/tmp/test"},
		ROBinds:    []string{"/tmp/skills"},
	}, "bash", "-c", "echo hello")

	if !strings.HasSuffix(cmd.Path, "bwrap") {
		t.Errorf("expected cmd.Path to end with bwrap, got %s", cmd.Path)
	}

	// First arg after bwrap should be --die-with-parent
	if len(cmd.Args) < 2 || cmd.Args[1] != "--die-with-parent" {
		t.Errorf("expected first bwrap arg to be --die-with-parent, got %v", cmd.Args)
	}

	// Last args should be "-- bash -c echo hello"
	n := len(cmd.Args)
	if cmd.Args[n-4] != "--" || filepath.Base(cmd.Args[n-3]) != "bash" || cmd.Args[n-2] != "-c" || cmd.Args[n-1] != "echo hello" {
		t.Errorf("expected trailing args to be '-- <bash> -c echo hello', got %v", cmd.Args[n-4:])
	}

	if cmd.Dir != "/tmp/test" {
		t.Errorf("expected Dir=/tmp/test, got %s", cmd.Dir)
	}
	if len(cmd.Env) != 1 || cmd.Env[0] != "HOME=/tmp/test" {
		t.Errorf("expected Env=['HOME=/tmp/test'], got %v", cmd.Env)
	}
}

func TestBwrapExecutor_Prepare_ResolvesCommandPath(t *testing.T) {
	dir := t.TempDir()
	commandPath := filepath.Join(dir, "customcmd")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	t.Setenv("PATH", dir)

	e := &BwrapExecutor{cfg: BwrapConfig{}, path: "/usr/bin/bwrap"}
	cmd := e.Prepare(ExecOptions{
		Ctx:        context.Background(),
		WorkingDir: "/tmp/test",
		Env:        []string{"PATH=" + dir},
		RWBinds:    []string{"/tmp/test"},
	}, "customcmd", "--flag")

	argsStr := strings.Join(cmd.Args, " ")
	if !strings.Contains(argsStr, "--ro-bind "+dir+" "+dir) {
		t.Fatalf("expected runtime bind for command directory, got %s", argsStr)
	}

	n := len(cmd.Args)
	if cmd.Args[n-2] != commandPath || cmd.Args[n-1] != "--flag" {
		t.Fatalf("expected resolved absolute command path, got %v", cmd.Args[n-2:])
	}
}

func TestRuntimeMountRoots(t *testing.T) {
	tests := []struct {
		name        string
		commandPath string
		want        []string
	}{
		{name: "usr local", commandPath: "/usr/local/bin/node", want: []string{"/usr/local"}},
		{name: "opt", commandPath: "/opt/tooling/bin/python3", want: []string{"/opt"}},
		{name: "nix", commandPath: "/nix/store/abc-python/bin/python3", want: []string{"/nix/store"}},
		{name: "custom", commandPath: "/custom/bin/mytool", want: []string{"/custom/bin"}},
		{name: "relative", commandPath: "python3", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := runtimeMountRoots(tt.commandPath)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("runtimeMountRoots(%q) = %v, want %v", tt.commandPath, got, tt.want)
			}
		})
	}
}

func TestBwrapExecutor_buildArgs_ProcBindFallback(t *testing.T) {
	e := &BwrapExecutor{cfg: BwrapConfig{ProcBindFallback: true}, path: "/usr/bin/bwrap"}

	args := e.buildArgs(ExecOptions{
		Ctx:        context.Background(),
		WorkingDir: "/tmp/test",
		Env:        os.Environ(),
		RWBinds:    []string{"/tmp/test"},
	}, nil)

	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "--bind /proc /proc") {
		t.Errorf("expected --bind /proc /proc in fallback mode, got: %s", argsStr)
	}
	if strings.Contains(argsStr, "--proc /proc") {
		t.Errorf("expected --proc /proc to be absent in fallback mode, got: %s", argsStr)
	}
}

func TestBwrapExecutor_buildArgs_DefaultProcMount(t *testing.T) {
	e := &BwrapExecutor{cfg: BwrapConfig{}, path: "/usr/bin/bwrap"}

	args := e.buildArgs(ExecOptions{
		Ctx:        context.Background(),
		WorkingDir: "/tmp/test",
		Env:        os.Environ(),
		RWBinds:    []string{"/tmp/test"},
	}, nil)

	argsStr := strings.Join(args, " ")
	if !strings.Contains(argsStr, "--proc /proc") {
		t.Errorf("expected --proc /proc in default mode, got: %s", argsStr)
	}
	if strings.Contains(argsStr, "--bind /proc /proc") {
		t.Errorf("expected --bind /proc /proc to be absent in default mode, got: %s", argsStr)
	}
}

func TestNewBwrapExecutor_NotFound(t *testing.T) {
	_, err := NewBwrapExecutor(BwrapConfig{BwrapPath: "/nonexistent/bwrap"})
	if err == nil {
		t.Error("expected error when bwrap not found")
	}
}
