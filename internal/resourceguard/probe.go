package resourceguard

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// cappedBuffer bounds telemetry output even if an unexpected provider emits
// more than requested. Commands also have deadlines and never collect logs.
type cappedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > b.limit {
		return 0, fmt.Errorf("telemetry exceeds %d bytes", b.limit)
	}
	return b.Buffer.Write(p)
}
func output(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, name, args...)
	var b cappedBuffer
	b.limit = 2 << 20
	c.Stdout = &b
	c.Stderr = &b
	err := c.Run()
	return b.String(), err
}
func number(s string) (float64, error) { return strconv.ParseFloat(strings.TrimSpace(s), 64) }
func processIdentity(ctx context.Context, pid int) (string, error) {
	out, err := output(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "", fmt.Errorf("process identity missing")
	}
	return strings.TrimSpace(out), nil
}

// Probe performs bounded native reads only. Missing required telemetry fails
// closed; lack of pmset warnings is never interpreted as healthy thermal state.
func Probe(ctx context.Context) (Sample, error) {
	s := Sample{At: time.Now(), CPUs: runtime.NumCPU()}
	var err error
	switch runtime.GOOS {
	case "darwin":
		err = probeDarwin(ctx, &s)
	case "linux":
		err = probeLinux(ctx, &s)
	default:
		err = fmt.Errorf("resource admission unsupported on %s", runtime.GOOS)
	}
	if err != nil {
		return s, err
	}
	s.Jobs, err = uncoveredWork(ctx)
	for _, job := range s.Jobs {
		s.Uncovered = append(s.Uncovered, job.description())
	}
	return s, err
}
func probeDarwin(ctx context.Context, s *Sample) error {
	out, err := output(ctx, "/usr/sbin/sysctl", "-n", "kern.memorystatus_vm_pressure_level")
	if err != nil {
		return err
	}
	s.MemoryNormal = strings.TrimSpace(out) == "1"
	out, err = output(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", `ObjC.import("Foundation"); $.NSProcessInfo.processInfo.thermalState`)
	if err != nil {
		return fmt.Errorf("thermal probe: %w", err)
	}
	thermal, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || thermal < 0 || thermal > 3 {
		return fmt.Errorf("invalid thermal state")
	}
	s.ThermalKnown = true
	s.ThermalHeavy = thermal >= 2
	out, err = output(ctx, "/usr/sbin/sysctl", "-n", "vm.loadavg")
	if err != nil {
		return err
	}
	fields := strings.Fields(strings.Trim(out, "{} \n"))
	if len(fields) < 1 {
		return fmt.Errorf("missing load average")
	}
	s.Load1, err = number(fields[0])
	if err != nil {
		return err
	}
	out, err = output(ctx, "/usr/sbin/sysctl", "-n", "kern.boottime")
	if err != nil {
		return err
	}
	m := regexp.MustCompile(`sec = ([0-9]+)`).FindStringSubmatch(out)
	if len(m) != 2 {
		return fmt.Errorf("missing boot time")
	}
	sec, _ := strconv.ParseInt(m[1], 10, 64)
	s.Uptime = time.Since(time.Unix(sec, 0))
	out, err = output(ctx, "/usr/bin/top", "-l", "2", "-s", "1", "-n", "0")
	if err != nil {
		return err
	}
	matches := regexp.MustCompile(`([0-9.]+)% idle`).FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return fmt.Errorf("CPU idle unavailable")
	}
	s.CPUIdle, err = number(matches[len(matches)-1][1])
	if err != nil {
		return err
	}
	entries, err := os.ReadDir("/Library/Logs/DiagnosticReports")
	if err != nil {
		return fmt.Errorf("watchdog evidence unavailable: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "WindowServer") || !strings.Contains(name, "watchdog") {
			continue
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		if time.Since(info.ModTime()) < recoveryWindow {
			s.RecentWatchdog = true
			break
		}
	}
	return nil
}
func cpuCounters() (idle, total uint64, err error) {
	b, e := os.ReadFile("/proc/stat")
	if e != nil {
		return 0, 0, e
	}
	line := strings.SplitN(string(b), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return 0, 0, fmt.Errorf("invalid CPU counters")
	}
	for i, f := range fields[1:] {
		v, e := strconv.ParseUint(f, 10, 64)
		if e != nil {
			return 0, 0, e
		}
		total += v
		if i == 3 || i == 4 {
			idle += v
		}
		if i == 7 {
			break
		}
	}
	return idle, total, nil
}
func probeLinux(ctx context.Context, s *Sample) error {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return err
	}
	s.Load1, err = number(strings.Fields(string(b))[0])
	if err != nil {
		return err
	}
	b, err = os.ReadFile("/proc/uptime")
	if err != nil {
		return err
	}
	up, err := number(strings.Fields(string(b))[0])
	if err != nil {
		return err
	}
	s.Uptime = time.Duration(up * float64(time.Second))
	b, err = os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return err
	}
	m := regexp.MustCompile(`full avg10=([0-9.]+)`).FindStringSubmatch(string(b))
	if len(m) != 2 {
		return fmt.Errorf("memory PSI missing")
	}
	psi, err := number(m[1])
	if err != nil {
		return err
	}
	s.MemoryNormal = psi < 1
	i, t, err := cpuCounters()
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(200 * time.Millisecond):
	}
	i2, t2, err := cpuCounters()
	if err != nil {
		return err
	}
	if t2 <= t {
		return fmt.Errorf("CPU sample unavailable")
	}
	s.CPUIdle = 100 * float64(i2-i) / float64(t2-t)
	zones, err := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	if err != nil {
		return err
	}
	for _, zone := range zones {
		b, e := os.ReadFile(zone)
		if e != nil {
			continue
		}
		v, e := number(string(b))
		if e != nil {
			continue
		}
		s.ThermalKnown = true
		if v >= 85000 {
			s.ThermalHeavy = true
		}
	}
	// No temperature source is explicitly unknown, rather than assumed healthy.
	return nil
}

type processRow struct {
	pid, ppid  int
	name, args string
	cpu        float64
}

func uncoveredWork(ctx context.Context) ([]HeavyJob, error) {
	out, err := output(ctx, "/bin/ps", "-axo", "pid=,ppid=,%cpu=,comm=,args=")
	if err != nil {
		return nil, fmt.Errorf("process inventory: %w", err)
	}
	var rows []processRow
	parents := map[int]int{}
	scan := bufio.NewScanner(strings.NewReader(out))
	scan.Buffer(make([]byte, 4096), 1<<20)
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) < 4 {
			continue
		}
		pid, e := strconv.Atoi(f[0])
		if e != nil {
			continue
		}
		ppid, e := strconv.Atoi(f[1])
		if e != nil {
			continue
		}
		cpu, _ := strconv.ParseFloat(f[2], 64)
		r := processRow{pid: pid, ppid: ppid, cpu: cpu, name: filepath.Base(f[3]), args: strings.Join(f[4:], " ")}
		rows = append(rows, r)
		parents[pid] = ppid
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	ancestors := map[int]bool{}
	for p := os.Getpid(); p > 0 && !ancestors[p]; p = parents[p] {
		ancestors[p] = true
	}
	var found []HeavyJob
	for _, r := range rows {
		if ancestors[r.pid] || r.ppid == os.Getpid() {
			continue
		}
		if heavyweight(r.name, r.args) || r.cpu >= 100 {
			found = append(found, HeavyJob{PID: r.pid, ParentPID: r.ppid, Name: r.name, Maintenance: containsWord(r.args, "install", "update", "upgrade", "sync", "ci")})
			if len(found) > 20 {
				return nil, fmt.Errorf("heavy process inventory exceeds 20 jobs; ownership cannot be established safely")
			}
		}
	}
	return resolveJobDirectories(ctx, found), nil
}
func heavyweight(name, args string) bool {
	switch name {
	case "rustc", "xcodebuild", "clang", "clang++", "cc1", "cc1plus", "ld", "ld64", "swift-frontend", "ninja", "make", "cmake":
		return true
	case "cargo":
		return containsWord(args, "build", "test", "check", "clippy", "install")
	case "go":
		return containsWord(args, "build", "test", "install", "generate")
	case "npm", "pnpm", "yarn", "bun", "uv", "pip", "pip3", "brew":
		return containsWord(args, "install", "update", "upgrade", "sync", "build", "test", "ci")
	case "cp", "rsync":
		return strings.Contains(args, "target") || strings.Contains(args, "node_modules") || strings.Contains(args, " -r") || strings.Contains(args, " -R") || strings.Contains(args, " -a") || strings.Contains(args, " -cR")
	case "node", "python", "python3":
		return containsWord(args, "vitest", "jest", "playwright", "pytest", "install", "upgrade", "embedding") || strings.Contains(args, "embedding")
	}
	return strings.HasPrefix(name, "maru_lib-") || strings.HasPrefix(name, "rustdoc")
}
func containsWord(s string, words ...string) bool {
	for _, field := range strings.Fields(s) {
		for _, word := range words {
			if field == word || filepath.Base(field) == word {
				return true
			}
		}
	}
	return false
}
