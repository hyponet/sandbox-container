package handler

import (
	"encoding/json"
	"os/exec"
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
