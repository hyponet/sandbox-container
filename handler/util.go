package handler

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hyponet/sandbox-container/audit"
	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/projectdata"
	"github.com/hyponet/sandbox-container/session"
	"github.com/hyponet/sandbox-container/userdata"
)

const defaultExecPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

const (
	SandboxRoot           = "/"
	SandboxHome           = "/home"
	SandboxSkillsDir      = "/agents/skills"
	SandboxProjectdataDir = "/home/project"
)

func validateOptionalUserID(userID string) error {
	if userID != "" {
		if err := validateRequiredID("user_id", userID); err != nil {
			return err
		}
	}
	return nil
}

func validateRequiredID(field, id string) error {
	if id == "" {
		return fmt.Errorf("%s is required", field)
	}
	if err := audit.ValidateID(id); err != nil {
		return fmt.Errorf("invalid %s: %w", field, err)
	}
	return nil
}

// resolvedRoots holds the resolved host paths for a request.
type resolvedRoots struct {
	HostRoot        string // session root or workspace root
	SkillsRoot      string // agent skills root
	UserdataRoot    string // user userdata root (empty if no userID)
	ProjectdataRoot string // project projectdata root (empty if no projectID)
}

// resolveRoots resolves the host paths based on workspace mode.
func resolveRoots(mgr *session.Manager, udMgr *userdata.Manager, pdMgr *projectdata.Manager, agentID, sessionID string, agentWorkspace bool, userID, projectID string) (resolvedRoots, error) {
	var roots resolvedRoots
	if agentWorkspace {
		mgr.TouchWorkspace(agentID)
		roots = resolvedRoots{
			HostRoot:   mgr.WorkspaceRoot(agentID),
			SkillsRoot: mgr.SkillsRoot(agentID),
		}
	} else {
		mgr.Touch(agentID, sessionID)
		roots = resolvedRoots{
			HostRoot:   mgr.SessionRoot(agentID, sessionID),
			SkillsRoot: mgr.SkillsRoot(agentID),
		}
	}
	if userID != "" {
		if err := udMgr.Touch(userID); err != nil {
			return roots, err
		}
		roots.UserdataRoot = udMgr.Root(userID)
		// Call userdataInit (e.g. create symlink in direct mode) for the session/workspace dir.
		if fn := udMgr.InitFn(); fn != nil {
			if err := fn(roots.HostRoot, roots.UserdataRoot); err != nil {
				return roots, err
			}
		}
	}
	if projectID != "" {
		if err := pdMgr.Touch(projectID); err != nil {
			return roots, err
		}
		roots.ProjectdataRoot = pdMgr.Root(projectID)
		if fn := pdMgr.InitFn(); fn != nil {
			if err := fn(roots.HostRoot, roots.ProjectdataRoot); err != nil {
				return roots, err
			}
		}
	}
	return roots, nil
}

// sandboxPathMapping holds the host-side roots corresponding to sandbox-visible mount points.
type sandboxPathMapping struct {
	HostRoot        string
	SkillsRoot      string
	UserdataRoot    string // empty means no userdata mapping
	ProjectdataRoot string // empty means no projectdata mapping
}

func sandboxPathMappingFromRoots(roots resolvedRoots) sandboxPathMapping {
	return sandboxPathMapping{
		HostRoot:        roots.HostRoot,
		SkillsRoot:      roots.SkillsRoot,
		UserdataRoot:    roots.UserdataRoot,
		ProjectdataRoot: roots.ProjectdataRoot,
	}
}

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

func hasSandboxPathPrefix(path, prefix string) bool {
	cleanPath := filepath.Clean(path)
	cleanPrefix := filepath.Clean(prefix)
	if cleanPath == cleanPrefix {
		return true
	}
	return strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanPrefix+string(os.PathSeparator))
}

// hostPathForSandboxPath resolves a sandbox-internal path to the corresponding host path.
func hostPathForSandboxPath(mapping sandboxPathMapping, sandboxPath string) string {
	cleanPath := filepath.Clean(sandboxPath)

	if hasSandboxPathPrefix(cleanPath, SandboxSkillsDir) {
		rel, err := filepath.Rel(SandboxSkillsDir, cleanPath)
		if err != nil {
			return ""
		}
		return filepath.Join(mapping.SkillsRoot, rel)
	}

	if mapping.ProjectdataRoot != "" && hasSandboxPathPrefix(cleanPath, SandboxProjectdataDir) {
		rel, err := filepath.Rel(SandboxProjectdataDir, cleanPath)
		if err != nil {
			return ""
		}
		return filepath.Join(mapping.ProjectdataRoot, rel)
	}

	if mapping.UserdataRoot != "" && hasSandboxPathPrefix(cleanPath, SandboxHome) {
		rel, err := filepath.Rel(SandboxHome, cleanPath)
		if err != nil {
			return ""
		}
		return filepath.Join(mapping.UserdataRoot, rel)
	}

	rel, err := filepath.Rel(SandboxRoot, cleanPath)
	if err != nil {
		return ""
	}
	return filepath.Join(mapping.HostRoot, rel)
}

func ensureSandboxWorkingDir(roots resolvedRoots, reqPath string) (string, error) {
	if err := validatePathRequirements(reqPath); err != nil {
		return "", err
	}

	sandboxPath, err := resolveSandboxPath(reqPath)
	if err != nil {
		return "", err
	}

	hostPath := hostPathForSandboxPath(sandboxPathMappingFromRoots(roots), sandboxPath)
	if hostPath == "" {
		return "", fmt.Errorf("failed to resolve working directory: %s", reqPath)
	}

	if hasSandboxPathPrefix(sandboxPath, SandboxSkillsDir) {
		if _, err := os.Stat(hostPath); err != nil {
			return "", err
		}
		return sandboxPath, nil
	}

	if err := os.MkdirAll(hostPath, 0755); err != nil {
		return "", err
	}
	return sandboxPath, nil
}

func commandExecBinds(roots resolvedRoots) (rwBinds []executor.BindMount, roBinds []executor.BindMount) {
	rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.HostRoot, Dest: SandboxRoot})
	roBinds = appendUniqueBindMount(roBinds, executor.BindMount{Src: roots.SkillsRoot, Dest: SandboxSkillsDir})
	if roots.UserdataRoot != "" {
		rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.UserdataRoot, Dest: SandboxHome})
	}
	if roots.ProjectdataRoot != "" {
		rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.ProjectdataRoot, Dest: SandboxProjectdataDir})
	}
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

func appendUniqueBindMount(mounts []executor.BindMount, mount executor.BindMount) []executor.BindMount {
	cleanSrc := filepath.Clean(mount.Src)
	cleanDest := filepath.Clean(mount.Dest)
	if cleanSrc == "." || cleanSrc == "" || cleanDest == "." || cleanDest == "" {
		return mounts
	}
	for _, existing := range mounts {
		if filepath.Clean(existing.Src) == cleanSrc && filepath.Clean(existing.Dest) == cleanDest {
			return mounts
		}
	}
	return append(mounts, mount)
}
