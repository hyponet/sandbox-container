package handler

import (
	"os"
	"strings"
	"testing"
)

func TestBuildIsolatedEnv_IsolationVars(t *testing.T) {
	workingDir := "/data/agents/test-agent/sessions/test-session"
	env := buildIsolatedEnv([]string{"PATH=/usr/bin", "HOME=/parent/home"}, workingDir, nil)

	expected := map[string]string{
		"HOME":                    workingDir,
		"PWD":                     workingDir,
		"XDG_CACHE_HOME":          workingDir + "/.cache",
		"XDG_CONFIG_HOME":         workingDir + "/.config",
		"XDG_DATA_HOME":           workingDir + "/.local/share",
		"XDG_STATE_HOME":          workingDir + "/.local/state",
		"PYTHONDONTWRITEBYTECODE": "1",
	}

	// Collect last values for each key (last-wins semantics)
	lastValues := map[string]string{}
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			lastValues[parts[0]] = parts[1]
		}
	}

	for key, want := range expected {
		got, ok := lastValues[key]
		if !ok {
			t.Errorf("missing env var %s", key)
			continue
		}
		if got != want {
			t.Errorf("env[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestBuildIsolatedEnv_UserOverride(t *testing.T) {
	workingDir := "/data/agents/test-agent/sessions/test-session"
	userEnv := map[string]string{
		"HOME":   "/custom/home",
		"MY_VAR": "hello",
	}
	env := buildIsolatedEnv([]string{"PATH=/usr/bin", "HOME=/parent/home"}, workingDir, userEnv)

	lastValues := map[string]string{}
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			lastValues[parts[0]] = parts[1]
		}
	}

	if v := lastValues["HOME"]; v != "/custom/home" {
		t.Errorf("user HOME override: got %q, want /custom/home", v)
	}
	if v := lastValues["MY_VAR"]; v != "hello" {
		t.Errorf("MY_VAR: got %q, want hello", v)
	}
}

func TestBuildIsolatedEnv_NilUserEnv(t *testing.T) {
	workingDir := "/tmp/test-session"
	env := buildIsolatedEnv([]string{"PATH=/usr/bin"}, workingDir, nil)

	if len(env) == 0 {
		t.Error("expected non-empty env slice")
	}

	hasHome := false
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			hasHome = true
		}
	}
	if !hasHome {
		t.Error("expected HOME to be set")
	}
}

func TestBuildIsolatedEnv_PreservesParentEnv(t *testing.T) {
	workingDir := "/tmp/test-session"
	env := buildIsolatedEnv(nil, workingDir, nil)

	hasPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
		}
	}
	if !hasPath && len(os.Environ()) > 0 {
		t.Error("expected PATH from parent env to be preserved")
	}
}
