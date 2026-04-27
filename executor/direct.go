package executor

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
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

// InitSession creates the skills symlink for direct execution mode.
// sessionDir is the session or workspace directory, skillsDir is the agent's skills directory.
func (d *DirectExecutor) InitSession(sessionDir, skillsDir string) {
	// Ensure skills directory exists
	if err := os.MkdirAll(skillsDir, 0755); err != nil {
		return
	}

	// Create skills symlink: <sessionDir>/skills -> <relative path to skillsDir>
	symlinkPath := filepath.Join(sessionDir, "skills")
	os.Remove(symlinkPath)

	relSkills, err := filepath.Rel(sessionDir, skillsDir)
	if err != nil {
		relSkills = skillsDir
	}
	os.Symlink(relSkills, symlinkPath)
}

// InitUserdata creates the userdata symlink for direct execution mode.
func (d *DirectExecutor) InitUserdata(sessionDir, userdataDir string) {
	if userdataDir == "" {
		return
	}
	if err := os.MkdirAll(userdataDir, 0755); err != nil {
		log.Printf("[ERROR] InitUserdata: failed to create %s: %v", userdataDir, err)
		return
	}
	symlinkPath := filepath.Join(sessionDir, "userdata")
	os.Remove(symlinkPath)
	relUserdata, err := filepath.Rel(sessionDir, userdataDir)
	if err != nil {
		relUserdata = userdataDir
	}
	os.Symlink(relUserdata, symlinkPath)
}
