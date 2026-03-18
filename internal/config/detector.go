package config

import (
	"os"
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
	// GPU/CUDA detection
	HasNVIDIAGPU bool   `json:"has_nvidia_gpu"`
	GPUModel     string `json:"gpu_model"`
	HasCUDA      bool   `json:"has_cuda"`
	CUDAHome     string `json:"cuda_home"`
	IsDGX        bool   `json:"is_dgx"`
}

// DetectSystem probes the current system and returns SystemInfo.
func DetectSystem() (*SystemInfo, error) {
	info := &SystemInfo{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
	}

	detectHostname(info)
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

func detectGPU(info *SystemInfo) {
	out, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return
	}
	info.HasNVIDIAGPU = true
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
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
