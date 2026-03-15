package config

import (
	"os/exec"
	"runtime"
	"strings"
)

// SystemInfo holds detected system information.
type SystemInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
	HasBrew  bool   `json:"has_brew"`
	BrewPath string `json:"brew_path"`
}

// DetectSystem probes the current system and returns SystemInfo.
func DetectSystem() (*SystemInfo, error) {
	info := &SystemInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	detectHostname(info)
	detectBrew(info)

	return info, nil
}

// SuggestProfile returns a profile name based on detected system.
func (s *SystemInfo) SuggestProfile() string {
	return "full"
}

func detectHostname(info *SystemInfo) {
	out, err := exec.Command("hostname").Output()
	if err != nil {
		info.Hostname = "unknown"
		return
	}
	info.Hostname = strings.TrimSpace(string(out))
}

func detectBrew(info *SystemInfo) {
	path, err := exec.LookPath("brew")
	if err != nil {
		return
	}
	info.HasBrew = true
	info.BrewPath = path
}
