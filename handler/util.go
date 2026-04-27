package handler

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hyponet/sandbox-container/executor"
	"github.com/hyponet/sandbox-container/session"
)

const defaultExecPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

const (
	SandboxHome        = "/home"
	SandboxSkillsDir   = "/home/skills"
	SandboxUserdataDir = "/home/userdata"
)

// resolvedRoots holds the resolved host paths for a request.
type resolvedRoots struct {
	HostRoot     string // session root or workspace root
	SkillsRoot   string // agent skills root
	UserdataRoot string // user userdata root (empty if no userID)
}

// resolveRoots resolves the host paths based on workspace mode.
func resolveRoots(mgr *session.Manager, agentID, sessionID string, agentWorkspace bool, userID string) (resolvedRoots, error) {
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
		if err := mgr.TouchUserdata(userID); err != nil {
			return roots, err
		}
		roots.UserdataRoot = mgr.UserdataRoot(userID)
		// Call userdataInit (e.g. create symlink in direct mode) for the session/workspace dir.
		if fn := mgr.UserdataInit(); fn != nil {
			fn(roots.HostRoot, roots.UserdataRoot)
		}
	}
	return roots, nil
}

// sandboxPathMapping holds the host-to-sandbox path mapping configuration.
type sandboxPathMapping struct {
	HostRoot     string
	SkillsRoot   string
	UserdataRoot string // empty means no userdata mapping
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

// hostToSandboxPath translates a host path to the sandbox-internal path.
// In direct mode it returns the path unchanged.
func hostToSandboxPath(isBwrap bool, mapping sandboxPathMapping, hostPath string) string {
	if !isBwrap {
		return hostPath
	}
	cleanPath := filepath.Clean(hostPath)
	cleanRoot := filepath.Clean(mapping.HostRoot)
	cleanSkills := filepath.Clean(mapping.SkillsRoot)

	if cleanPath == cleanRoot || strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanRoot+string(os.PathSeparator)) {
		rel, err := filepath.Rel(cleanRoot, cleanPath)
		if err != nil {
			return hostPath
		}
		return filepath.Join(SandboxHome, rel)
	}

	if cleanPath == cleanSkills || strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanSkills+string(os.PathSeparator)) {
		rel, err := filepath.Rel(cleanSkills, cleanPath)
		if err != nil {
			return hostPath
		}
		return filepath.Join(SandboxSkillsDir, rel)
	}

	if mapping.UserdataRoot != "" {
		cleanUserdata := filepath.Clean(mapping.UserdataRoot)
		if cleanPath == cleanUserdata || strings.HasPrefix(cleanPath+string(os.PathSeparator), cleanUserdata+string(os.PathSeparator)) {
			rel, err := filepath.Rel(cleanUserdata, cleanPath)
			if err != nil {
				return hostPath
			}
			return filepath.Join(SandboxUserdataDir, rel)
		}
	}

	return hostPath
}

func commandExecBinds(roots resolvedRoots, agentWorkspace, isBwrap bool) (rwBinds []executor.BindMount, roBinds []executor.BindMount) {
	if isBwrap {
		rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.HostRoot, Dest: SandboxHome})
		roBinds = appendUniqueBindMount(roBinds, executor.BindMount{Src: roots.SkillsRoot, Dest: SandboxSkillsDir})
		if agentWorkspace {
			// workspace mode also needs skills writable
			rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.SkillsRoot, Dest: SandboxSkillsDir})
			roBinds = nil
		}
		if roots.UserdataRoot != "" {
			rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.UserdataRoot, Dest: SandboxUserdataDir})
		}
		return rwBinds, roBinds
	}

	// Direct mode: identity mapping (Src == Dest).
	// In workspace mode, skills are writable so they go into rwBinds (no roBinds needed).
	rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.HostRoot, Dest: roots.HostRoot})
	if agentWorkspace {
		rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.SkillsRoot, Dest: roots.SkillsRoot})
	} else {
		roBinds = appendUniqueBindMount(roBinds, executor.BindMount{Src: roots.SkillsRoot, Dest: roots.SkillsRoot})
	}
	if roots.UserdataRoot != "" {
		rwBinds = appendUniqueBindMount(rwBinds, executor.BindMount{Src: roots.UserdataRoot, Dest: roots.UserdataRoot})
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
