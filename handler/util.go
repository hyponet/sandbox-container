package handler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func strPtr(s string) *string { return &s }

func getHostname() (string, error) {
	h, err := exec.Command("hostname").Output()
	return strings.TrimSpace(string(h)), err
}

func getCurrentUser() string {
	u, err := exec.Command("whoami").Output()
	if err != nil {
		return "root"
	}
	return strings.TrimSpace(string(u))
}

func getTimezone() string {
	return time.Now().Location().String()
}

func getOSVersion() string {
	b, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}

func getPythonVersion() string {
	b, err := exec.Command("python3", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(string(b), "Python "))
}

func getNodeVersion() string {
	b, err := exec.Command("node", "--version").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func getInstalledPackages(cmd string) (interface{}, error) {
	parts := strings.Fields(cmd)
	out, err := exec.Command(parts[0], parts[1:]...).Output()
	if err != nil {
		return []string{}, nil
	}
	var result interface{}
	json.Unmarshal(out, &result)
	return result, nil
}

// buildIsolatedEnv constructs an environment variable slice with session isolation.
// Layering order: baseEnv -> isolation overrides -> user overrides.
func buildIsolatedEnv(baseEnv []string, workingDir string, userEnv map[string]string) []string {
	if baseEnv == nil {
		baseEnv = os.Environ()
	}

	env := append([]string(nil), baseEnv...)
	pwdDir := workingDir
	if resolved, err := filepath.EvalSymlinks(workingDir); err == nil {
		pwdDir = resolved
	} else if absDir, err := filepath.Abs(workingDir); err == nil {
		pwdDir = absDir
	}

	isolation := []string{
		"HOME=" + workingDir,
		"PWD=" + pwdDir,
		"XDG_CACHE_HOME=" + filepath.Join(workingDir, ".cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(workingDir, ".config"),
		"XDG_DATA_HOME=" + filepath.Join(workingDir, ".local", "share"),
		"XDG_STATE_HOME=" + filepath.Join(workingDir, ".local", "state"),
		"PYTHONDONTWRITEBYTECODE=1",
	}
	env = append(env, isolation...)

	for k, v := range userEnv {
		env = append(env, k+"="+v)
	}

	return env
}
