package config

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	execrun "github.com/entelecheia/dotfiles-v2/internal/exec"
)

// detectRunner is a shared read-only runner for probe commands.
func detectRunner() *execrun.Runner { return execrun.NewRunner(false, slog.Default()) }

// SystemInfo holds detected system information.
type SystemInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	Hostname string `json:"hostname"`
	HasBrew  bool   `json:"has_brew"`
	BrewPath string `json:"brew_path"`
	// GPU/CUDA detection
	HasNVIDIAGPU bool   `json:"has_nvidia_gpu"`
	GPUModel     string `json:"gpu_model"`
	HasCUDA      bool   `json:"has_cuda"`
	CUDAHome     string `json:"cuda_home"`
	IsDGX        bool   `json:"is_dgx"`
	// Shell/Git detection
	Shell      string `json:"shell"`
	HasGit     bool   `json:"has_git"`
	GitVersion string `json:"git_version"`
}

// DetectSystem probes the current system and returns SystemInfo.
func DetectSystem() (*SystemInfo, error) {
	info := &SystemInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	detectHostname(info)
	detectShell(info)
	detectGit(info)
	detectBrew(info)
	detectGPU(info)
	detectCUDA(info)
	detectDGX(info)

	return info, nil
}

// SuggestProfile returns a profile name based on detected system.
func (s *SystemInfo) SuggestProfile() string {
	if s.OS == "linux" && (s.HasNVIDIAGPU || s.HasCUDA || s.IsDGX) {
		return "server"
	}
	return "full"
}

func detectHostname(info *SystemInfo) {
	res, err := detectRunner().RunQuery(context.Background(), "hostname")
	if err != nil {
		info.Hostname = "unknown"
		return
	}
	info.Hostname = strings.TrimSpace(res.Stdout)
}

func detectBrew(info *SystemInfo) {
	path, err := exec.LookPath("brew")
	if err != nil {
		return
	}
	info.HasBrew = true
	info.BrewPath = path
}

func detectGPU(info *SystemInfo) {
	res, err := detectRunner().RunQuery(context.Background(), "nvidia-smi", "--query-gpu=name", "--format=csv,noheader,nounits")
	if err != nil {
		return
	}
	info.HasNVIDIAGPU = true
	lines := strings.SplitN(strings.TrimSpace(res.Stdout), "\n", 2)
	if len(lines) > 0 {
		info.GPUModel = strings.TrimSpace(lines[0])
	}
}

func detectCUDA(info *SystemInfo) {
	for _, p := range []string{"/usr/local/cuda", "/usr/cuda"} {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			info.HasCUDA = true
			info.CUDAHome = p
			return
		}
	}
}

func detectDGX(info *SystemInfo) {
	if _, err := os.Stat("/etc/dgx-release"); err == nil {
		info.IsDGX = true
	}
}

func detectShell(info *SystemInfo) {
	shell := os.Getenv("SHELL")
	if shell != "" {
		info.Shell = filepath.Base(shell)
		return
	}
	info.Shell = "unknown"
}

func detectGit(info *SystemInfo) {
	res, err := detectRunner().RunQuery(context.Background(), "git", "--version")
	if err != nil {
		return
	}
	info.HasGit = true
	// parse "git version 2.43.0" -> "2.43.0"
	parts := strings.Fields(strings.TrimSpace(res.Stdout))
	if len(parts) >= 3 {
		info.GitVersion = parts[2]
	}
}
