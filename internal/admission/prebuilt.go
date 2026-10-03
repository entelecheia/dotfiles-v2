package admission

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// PrebuiltSafetyWindow is the minimum observed interval required before a
// verified native prebuilt update may use the warning-tolerant profile.
const PrebuiltSafetyWindow = 30 * time.Second

// PrebuiltHistoryFile separates short update decisions from heavy-work
// recovery history. A warning-tolerant decision can never clear a heavy defer.
const PrebuiltHistoryFile = "history-prebuilt.json"

// PrebuiltThresholds uses immediate hard blockers and lets the independent
// 30-second swap-quiet window provide continuous recovery for this operation.
func PrebuiltThresholds() Thresholds {
	th := DefaultThresholds()
	th.IdleDeferBelow = 30
	th.IdleRecoverAt = 30
	th.LoadDeferFrac = 0.7
	th.LoadRecoverFrac = 0.7
	th.DeferSustain = 0
	th.RecoverSustain = 0
	return th
}

// SnapshotPrebuilt gathers the ordinary pressure probes and cumulative VM
// swap counters. It adds evidence without changing the raw pressure fields.
func SnapshotPrebuilt(ctx context.Context, m *Monitor) PressureSnapshot {
	snap := m.SnapshotPressure(ctx)
	var swapIn, swapOut uint64
	var ok bool
	switch snap.Platform {
	case "darwin":
		if m.Runner != nil {
			if result, err := m.query(ctx, "vm_stat"); err == nil {
				swapIn, swapOut, ok = parseDarwinSwapStats(result.Stdout)
			}
		}
	case "linux":
		swapIn, swapOut, ok = linuxSwapStats()
	default:
		return snap
	}
	snap.VMActivityAvailable = ok
	snap.SwapInAvailable = ok
	snap.SwapOutAvailable = ok
	if ok {
		snap.SwapInBytes = swapIn
		snap.SwapOutBytes = swapOut
	}
	return snap
}

// EvaluatePrebuiltPressure permits memory warning only after the complete
// sample window is healthy. Critical pressure, missing probes, serious or
// critical thermal state, recent WindowServer events, CPU limits, and any
// swap activity remain hard deferrals.
func EvaluatePrebuiltPressure(snap PressureSnapshot, th Thresholds, hist History, now time.Time) Decision {
	th.DeferSustain = 0
	th.RecoverSustain = 0
	check := snap
	if check.MemoryLevel == MemoryWarn {
		check.MemoryLevel = MemoryNormal
	}
	d := EvaluatePressure(check, th, hist, now)
	d.Next.LastSampleAt = now
	resetWindow := func() {
		d.Next.PrebuiltSwapAt = time.Time{}
		d.Next.PrebuiltSwapInBytes = 0
		d.Next.PrebuiltSwapOutBytes = 0
	}
	if !snap.MemoryAvailable || (snap.MemoryLevel != MemoryNormal && snap.MemoryLevel != MemoryWarn && snap.MemoryLevel != MemoryCritical) {
		if d.Admit {
			d.Admit = false
			d.Reasons = []string{"telemetry unavailable: memory pressure level"}
			d.RetryAfter = 30 * time.Second
		}
		resetWindow()
		return d
	}
	if !d.Admit {
		resetWindow()
		return d
	}
	if !snap.VMActivityAvailable || !snap.SwapInAvailable || !snap.SwapOutAvailable {
		resetWindow()
		d.Admit = false
		d.Reasons = []string{"telemetry unavailable: VM swap activity"}
		d.RetryAfter = 30 * time.Second
		return d
	}
	maxGap := th.MaxSampleGap
	if maxGap <= 0 {
		maxGap = DefaultMaxSampleGap
	}
	gap := now.Sub(hist.LastSampleAt)
	if hist.LastSampleAt.IsZero() || now.Before(hist.LastSampleAt) || gap <= 0 || gap > maxGap {
		d.Next.PrebuiltSwapInBytes = snap.SwapInBytes
		d.Next.PrebuiltSwapOutBytes = snap.SwapOutBytes
		d.Next.PrebuiltSwapAt = now
		d.Admit = false
		d.ProfileRetry = true
		d.Reasons = []string{"prebuilt safety window needs fresh samples"}
		d.RetryAfter = 15 * time.Second
		return d
	}
	if hist.PrebuiltSwapAt.IsZero() {
		d.Next.PrebuiltSwapInBytes = snap.SwapInBytes
		d.Next.PrebuiltSwapOutBytes = snap.SwapOutBytes
		d.Next.PrebuiltSwapAt = now
		d.Admit = false
		d.ProfileRetry = true
		d.Reasons = []string{"prebuilt safety window needs 30 seconds without swap activity"}
		d.RetryAfter = 15 * time.Second
		return d
	}
	if snap.SwapOutBytes != hist.PrebuiltSwapOutBytes || snap.SwapInBytes != hist.PrebuiltSwapInBytes {
		d.Next.PrebuiltSwapInBytes = snap.SwapInBytes
		d.Next.PrebuiltSwapOutBytes = snap.SwapOutBytes
		d.Next.PrebuiltSwapAt = now
		d.Admit = false
		d.ProfileRetry = true
		d.Reasons = []string{"swap activity; restarting the 30-second safety window"}
		d.RetryAfter = PrebuiltSafetyWindow
		return d
	}
	remaining := PrebuiltSafetyWindow - now.Sub(hist.PrebuiltSwapAt)
	if remaining > 0 {
		d.Admit = false
		d.ProfileRetry = true
		d.Reasons = []string{"prebuilt safety window needs 30 seconds without swap activity"}
		d.RetryAfter = remaining
		return d
	}
	d.Next.PrebuiltSwapInBytes = snap.SwapInBytes
	d.Next.PrebuiltSwapOutBytes = snap.SwapOutBytes
	d.Next.PrebuiltSwapAt = hist.PrebuiltSwapAt
	if d.Admit && snap.MemoryLevel == MemoryWarn {
		d.Reasons = append(d.Reasons, "allowed memory warning under verified prebuilt policy")
	}
	return d
}

func parseDarwinSwapStats(output string) (swapInBytes, swapOutBytes uint64, ok bool) {
	pageSize := uint64(0)
	var swapins, swapouts uint64
	var haveIn, haveOut bool
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if value, found := strings.CutPrefix(line, "Mach Virtual Memory Statistics:"); found {
			if i := strings.Index(value, "page size of "); i >= 0 {
				fields := strings.Fields(value[i+len("page size of "):])
				if len(fields) > 0 {
					pageSize, _ = strconv.ParseUint(fields[0], 10, 64)
				}
			}
			continue
		}
		if value, found := strings.CutPrefix(line, "Swapins:"); found {
			swapins, haveIn = parseVMStatCount(value)
		}
		if value, found := strings.CutPrefix(line, "Swapouts:"); found {
			swapouts, haveOut = parseVMStatCount(value)
		}
	}
	if pageSize == 0 || !haveIn || !haveOut || swapins > ^uint64(0)/pageSize || swapouts > ^uint64(0)/pageSize {
		return 0, 0, false
	}
	return swapins * pageSize, swapouts * pageSize, true
}

func parseVMStatCount(value string) (uint64, bool) {
	value = strings.TrimSpace(strings.TrimSuffix(value, "."))
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}

func linuxSwapStats() (swapInBytes, swapOutBytes uint64, ok bool) {
	data, err := os.ReadFile("/proc/vmstat")
	if err != nil {
		return 0, 0, false
	}
	var in, out uint64
	var haveIn, haveOut bool
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		n, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "pswpin":
			in, haveIn = n, true
		case "pswpout":
			out, haveOut = n, true
		}
	}
	pageSize := uint64(os.Getpagesize())
	if !haveIn || !haveOut || pageSize == 0 || in > ^uint64(0)/pageSize || out > ^uint64(0)/pageSize {
		return 0, 0, false
	}
	return in * pageSize, out * pageSize, true
}
