package executor

import (
	"context"
	"os/exec"
)

// ExecOptions contains everything needed to prepare a command for execution.
type ExecOptions struct {
	Ctx        context.Context
	WorkingDir string
	Env        []string

	// Paths the sandbox needs access to (read-write).
	RWBinds []string
	// Paths the sandbox needs access to (read-only).
	ROBinds []string
}

// CommandExecutor abstracts command creation so handlers don't know
// whether they are running bare or inside bwrap.
type CommandExecutor interface {
	// Prepare builds an *exec.Cmd ready to be started.
	// The caller still owns Start/Wait/StdinPipe/etc.
	Prepare(opts ExecOptions, name string, args ...string) *exec.Cmd
}
