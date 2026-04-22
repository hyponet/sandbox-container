package handler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hyponet/sandbox-container/session"
)

const defaultExecPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

var sensitiveExecEnvKeys = map[string]struct{}{
	"ANTHROPIC_API_KEY":     {},
	"AWS_ACCESS_KEY_ID":     {},
	"AWS_SECRET_ACCESS_KEY": {},
	"AWS_SESSION_TOKEN":     {},
	"AZURE_OPENAI_API_KEY":  {},
	"GITHUB_TOKEN":          {},
	"GITLAB_TOKEN":          {},
	"GH_TOKEN":              {},
	"HF_TOKEN":              {},
	"HUGGINGFACE_HUB_TOKEN": {},
	"NPM_TOKEN":             {},
	"OPENAI_API_KEY":        {},
	"PYPI_TOKEN":            {},
	"SANDBOX_API_KEY":       {},
	"TWINE_PASSWORD":        {},
}

var sensitiveExecEnvSuffixes = []string{
	"_ACCESS_TOKEN",
	"_API_KEY",
	"_PASSWORD",
	"_PRIVATE_KEY",
	"_SECRET",
	"_SECRET_ACCESS_KEY",
	"_SECRET_KEY",
	"_TOKEN",
}

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
	env := filteredBaseEnv(baseEnv)
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

func commandExecBinds(mgr *session.Manager, agentID, writableRoot string, agentWorkspace bool) (rwBinds []string, roBinds []string) {
	rwBinds = appendUniqueExecPath(rwBinds, writableRoot)

	skillsRoot := mgr.SkillsRoot(agentID)
	if agentWorkspace {
		rwBinds = appendUniqueExecPath(rwBinds, skillsRoot)
		return rwBinds, nil
	}

	roBinds = appendUniqueExecPath(roBinds, skillsRoot)
	return rwBinds, roBinds
}

func filteredBaseEnv(baseEnv []string) []string {
	if baseEnv == nil {
		baseEnv = os.Environ()
	}

	env := make([]string, 0, len(baseEnv)+1)
	hasPath := false
	for _, entry := range baseEnv {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || isSensitiveExecEnvKey(key) {
			continue
		}
		if key == "PATH" && strings.TrimSpace(value) != "" {
			hasPath = true
		}
		env = append(env, entry)
	}
	if !hasPath {
		env = append(env, "PATH="+defaultExecPath)
	}
	return env
}

func isSensitiveExecEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	if _, ok := sensitiveExecEnvKeys[upper]; ok {
		return true
	}
	if strings.HasPrefix(upper, "BASH_FUNC_") {
		return true
	}
	for _, suffix := range sensitiveExecEnvSuffixes {
		if strings.HasSuffix(upper, suffix) {
			return true
		}
	}
	return false
}

func appendUniqueExecPath(paths []string, path string) []string {
	cleanPath := filepath.Clean(path)
	if cleanPath == "." || cleanPath == "" {
		return paths
	}
	for _, existing := range paths {
		if filepath.Clean(existing) == cleanPath {
			return paths
		}
	}
	return append(paths, cleanPath)
}
