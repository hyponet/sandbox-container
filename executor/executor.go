package executor

import (
	"context"
	"os/exec"
)

// BindMount represents a bind mount mapping from a host path to a sandbox path.
type BindMount struct {
	Src  string // host path
	Dest string // sandbox-internal path
}

// ExecOptions contains everything needed to prepare a command for execution.
type ExecOptions struct {
	Ctx        context.Context
	WorkingDir string
	Env        []string

	// Paths the sandbox needs access to (read-write).
	RWBinds []BindMount
	// Paths the sandbox needs access to (read-only).
	ROBinds []BindMount
}

// CommandExecutor abstracts command creation so handlers don't know
// whether they are running bare or inside bwrap.
type CommandExecutor interface {
	// Prepare builds an *exec.Cmd ready to be started.
	// The caller still owns Start/Wait/StdinPipe/etc.
	Prepare(opts ExecOptions, name string, args ...string) *exec.Cmd

	// InitSession is called after the session/workspace directory is created.
	// It performs executor-specific initialization (e.g. symlinks for DirectExecutor).
	InitSession(sessionDir, skillsDir string)

	// InitUserdata is called when a userID is provided to set up userdata access.
	InitUserdata(sessionDir, userdataDir string)
}
