package executor

import (
	"os/exec"
)

// DirectExecutor passes through to exec.CommandContext directly.
// This preserves the current (non-bwrap) behavior.
type DirectExecutor struct{}

func (d *DirectExecutor) Prepare(opts ExecOptions, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(opts.Ctx, name, args...)
	cmd.Dir = opts.WorkingDir
	cmd.Env = opts.Env
	return cmd
}
