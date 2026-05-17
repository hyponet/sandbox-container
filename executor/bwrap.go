package executor

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
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

// InitSession creates required subdirectories inside the session/workspace directory.
func (b *BwrapExecutor) InitSession(sessionDir, skillsDir string) {
	os.MkdirAll(filepath.Join(sessionDir, "home"), 0755)
	os.MkdirAll(filepath.Join(sessionDir, "agents"), 0755)
}

// InitUserdata is a no-op for bwrap mode (userdata access is handled via bind mounts).
func (b *BwrapExecutor) InitUserdata(sessionDir, userdataDir string) error { return nil }

// InitProjectdata is a no-op for bwrap mode (projectdata access is handled via bind mounts).
func (b *BwrapExecutor) InitProjectdata(sessionDir, projectdataDir string) error { return nil }

// buildArgs constructs the bwrap argument list (everything before "--").
// Mount order is parent-first: / → system paths → /home → /home/project → /agents/skills.
func (b *BwrapExecutor) buildArgs(opts ExecOptions, runtimeROBinds []string) []string {
	args := []string{
		"--die-with-parent",
		"--new-session",
		"--unshare-all",
	}

	// Network isolation (optional)
	if b.cfg.NetworkMode != "isolated" {
		args = append(args, "--share-net")
	}

	seen := map[string]struct{}{}

	// 1. Root RW bind (session/workspace at /) — must be first.
	for _, rw := range opts.RWBinds {
		if filepath.Clean(rw.Dest) == "/" {
			args = appendBind(args, seen, "--bind", rw.Src, rw.Dest)
		}
	}

	// 2. System paths: read-only
	systemPaths := []string{"/usr", "/lib", "/lib64", "/bin", "/sbin", "/etc"}
	for _, p := range systemPaths {
		if _, err := os.Stat(p); err == nil {
			args = appendBind(args, seen, "--ro-bind", p, p)
		}
	}

	// 3. /dev and /proc
	args = append(args, "--dev", "/dev")
	if b.cfg.ProcBindFallback {
		args = append(args, "--bind", "/proc", "/proc")
	} else {
		args = append(args, "--proc", "/proc")
	}

	// 4. Per-execution tmpfs for /tmp
	args = append(args, "--tmpfs", "/tmp")

	// 5. Remaining RW binds sorted by dest path depth (parent first: /home before /home/project)
	remainingRW := make([]BindMount, 0, len(opts.RWBinds))
	for _, rw := range opts.RWBinds {
		if filepath.Clean(rw.Dest) != "/" {
			remainingRW = append(remainingRW, rw)
		}
	}
	sort.Slice(remainingRW, func(i, j int) bool {
		return len(filepath.Clean(remainingRW[i].Dest)) < len(filepath.Clean(remainingRW[j].Dest))
	})
	for _, rw := range remainingRW {
		args = appendBind(args, seen, "--bind", rw.Src, rw.Dest)
	}

	// 6. Read-only binds from opts (skills dir).
	for _, ro := range opts.ROBinds {
		args = appendBind(args, seen, "--ro-bind", ro.Src, ro.Dest)
	}

	// 7. Read-only binds required by resolved runtime paths.
	for _, ro := range runtimeROBinds {
		args = appendBind(args, seen, "--ro-bind", ro, ro)
	}

	// 8. Extra read-only binds from config
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

	key := flag + ":" + cleanSrc + "-" + cleanDest
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
