package handler

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildIsolatedEnv_FiltersSensitiveVars(t *testing.T) {
	workingDir := filepath.Join(t.TempDir(), "work")
	env := buildIsolatedEnv([]string{
		"PATH=/usr/bin:/bin",
		"LANG=en_US.UTF-8",
		"LC_ALL=C.UTF-8",
		"TERM=xterm-256color",
		"HTTP_PROXY=http://proxy.internal:8080",
		"SSL_CERT_FILE=/tmp/custom-ca.pem",
		"SANDBOX_API_KEY=top-secret",
		"AWS_SECRET_ACCESS_KEY=also-secret",
	}, workingDir, nil)

	envText := strings.Join(env, "\n")
	if strings.Contains(envText, "SANDBOX_API_KEY=") {
		t.Fatalf("expected SANDBOX_API_KEY to be filtered, got %v", env)
	}
	if strings.Contains(envText, "AWS_SECRET_ACCESS_KEY=") {
		t.Fatalf("expected AWS_SECRET_ACCESS_KEY to be filtered, got %v", env)
	}
	if !strings.Contains(envText, "PATH=/usr/bin:/bin") {
		t.Fatalf("expected PATH to be preserved, got %v", env)
	}
	if !strings.Contains(envText, "HTTP_PROXY=http://proxy.internal:8080") {
		t.Fatalf("expected HTTP_PROXY to be preserved, got %v", env)
	}
	if !strings.Contains(envText, "SSL_CERT_FILE=/tmp/custom-ca.pem") {
		t.Fatalf("expected SSL_CERT_FILE to be preserved, got %v", env)
	}
	if !strings.Contains(envText, "HOME="+workingDir) {
		t.Fatalf("expected HOME override, got %v", env)
	}
}

func TestBuildIsolatedEnv_DefaultsPathWhenMissing(t *testing.T) {
	workingDir := t.TempDir()
	env := buildIsolatedEnv([]string{"LANG=C.UTF-8"}, workingDir, nil)

	envText := strings.Join(env, "\n")
	if !strings.Contains(envText, "PATH="+defaultExecPath) {
		t.Fatalf("expected default PATH, got %v", env)
	}
}

func TestBuildIsolatedEnv_FiltersExportedShellFunctions(t *testing.T) {
	workingDir := t.TempDir()
	env := buildIsolatedEnv([]string{
		"BASH_FUNC_module%%=() {  eval `/usr/bin/modulecmd bash $*`\n}",
		"PATH=/usr/bin:/bin",
	}, workingDir, nil)

	envText := strings.Join(env, "\n")
	if strings.Contains(envText, "BASH_FUNC_module%%=") {
		t.Fatalf("expected exported shell functions to be filtered, got %v", env)
	}
}
