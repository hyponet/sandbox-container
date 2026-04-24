package executor

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// BwrapConfig holds configuration for the bubblewrap sandbox.
type BwrapConfig struct {
	// BwrapPath is the path to the bwrap binary. Defaults to "bwrap".
	BwrapPath string
	// NetworkMode controls network isolation: "host" (default) or "isolated".
	NetworkMode string
	// ExtraROBinds are additional read-only bind mount paths.
	ExtraROBinds []string
	// ProcBindFallback uses --bind /proc /proc instead of --proc /proc
	// for systems where new procfs mounts are restricted.
	ProcBindFallback bool
}

// BwrapExecutor wraps command execution in a bubblewrap sandbox.
type BwrapExecutor struct {
	cfg  BwrapConfig
	path string // resolved absolute path to bwrap binary
}

// NewBwrapExecutor creates a BwrapExecutor after validating that bwrap is available.
func NewBwrapExecutor(cfg BwrapConfig) (*BwrapExecutor, error) {
	binPath := cfg.BwrapPath
	if binPath == "" {
		binPath = "bwrap"
	}
	resolved, err := exec.LookPath(binPath)
	if err != nil {
		return nil, fmt.Errorf("bwrap binary not found at %q: %w", binPath, err)
	}
	return &BwrapExecutor{cfg: cfg, path: resolved}, nil
}

// Prepare builds an *exec.Cmd that runs the given command inside a bwrap sandbox.
func (b *BwrapExecutor) Prepare(opts ExecOptions, name string, args ...string) *exec.Cmd {
	resolvedName, runtimeROBinds := b.resolveCommandPath(name)
	bwrapArgs := b.buildArgs(opts, runtimeROBinds)
	bwrapArgs = append(bwrapArgs, "--")
	bwrapArgs = append(bwrapArgs, resolvedName)
	bwrapArgs = append(bwrapArgs, args...)

	log.Printf("[bwrap] %s %s", b.path, strings.Join(bwrapArgs, " "))

	cmd := exec.CommandContext(opts.Ctx, b.path, bwrapArgs...)
	cmd.Dir = "/"
	cmd.Env = opts.Env
	return cmd
}

// InitSession is a no-op for bwrap mode (skills access is handled via bind mounts).
func (b *BwrapExecutor) InitSession(sessionDir, skillsDir string) {}

// buildArgs constructs the bwrap argument list (everything before "--").
func (b *BwrapExecutor) buildArgs(opts ExecOptions, runtimeROBinds []string) []string {
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-pid",
		"--unshare-uts",
		"--unshare-ipc",
	}

	// Network isolation (optional)
	if b.cfg.NetworkMode == "isolated" {
		args = append(args, "--unshare-net")
	}

	// System paths: read-only
	systemPaths := []string{"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc"}
	seen := map[string]struct{}{}
	for _, p := range systemPaths {
		if _, err := os.Stat(p); err == nil {
			args = appendBind(args, seen, "--ro-bind", p, p)
		}
	}

	// /dev and /proc
	args = append(args, "--dev", "/dev")
	if b.cfg.ProcBindFallback {
		args = append(args, "--bind", "/proc", "/proc")
	} else {
		args = append(args, "--proc", "/proc")
	}

	// Per-execution tmpfs for /tmp
	args = append(args, "--tmpfs", "/tmp")

	// Read-write binds from opts (session/workspace dir)
	for _, rw := range opts.RWBinds {
		args = appendBind(args, seen, "--bind", rw.Src, rw.Dest)
	}

	// Read-only binds required by resolved runtime paths.
	for _, ro := range runtimeROBinds {
		args = appendBind(args, seen, "--ro-bind", ro, ro)
	}

	// Read-only binds from opts (skills dir).
	for _, ro := range opts.ROBinds {
		args = appendBind(args, seen, "--ro-bind", ro.Src, ro.Dest)
	}

	// Extra read-only binds from config
	for _, ro := range b.cfg.ExtraROBinds {
		if _, err := os.Stat(ro); err == nil {
			args = appendBind(args, seen, "--ro-bind", ro, ro)
		}
	}

	// Set working directory inside the sandbox
	args = append(args, "--chdir", opts.WorkingDir)

	return args
}

func (b *BwrapExecutor) resolveCommandPath(name string) (string, []string) {
	if name == "" {
		return name, nil
	}

	if strings.ContainsRune(name, os.PathSeparator) {
		absName := name
		if !filepath.IsAbs(absName) {
			if resolved, err := filepath.Abs(absName); err == nil {
				absName = resolved
			}
		}
		return absName, runtimeMountRoots(absName)
	}

	resolved, err := exec.LookPath(name)
	if err != nil {
		return name, nil
	}
	return resolved, runtimeMountRoots(resolved)
}

func runtimeMountRoots(commandPath string) []string {
	cleanPath := filepath.Clean(commandPath)
	if !filepath.IsAbs(cleanPath) {
		return nil
	}

	for _, root := range []string{"/usr/local", "/opt", "/run/current-system", "/nix/store"} {
		if hasPathPrefix(cleanPath, root) {
			return []string{root}
		}
	}

	return []string{filepath.Dir(commandPath)}
}

func appendBind(args []string, seen map[string]struct{}, flag, src, dest string) []string {
	cleanSrc := filepath.Clean(src)
	cleanDest := filepath.Clean(dest)
	if cleanSrc == "." || cleanSrc == "" || cleanDest == "." || cleanDest == "" {
		return args
	}

	key := cleanSrc + "-" + cleanDest
	if _, ok := seen[key]; ok {
		return args
	}
	seen[key] = struct{}{}

	return append(args, flag, cleanSrc, cleanDest)
}

func hasPathPrefix(path, prefix string) bool {
	cleanPath := filepath.Clean(path)
	cleanPrefix := filepath.Clean(prefix)
	if cleanPath == cleanPrefix {
		return true
	}
	return strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanPrefix+string(os.PathSeparator))
}
