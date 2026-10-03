package admission

import (
	"strings"
	"testing"
	"time"
)

func TestEvaluatePrebuiltPressureRequiresContinuousSwapQuietWindow(t *testing.T) {
	now := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	snap := PressureSnapshot{
		Platform: "darwin", MemoryLevel: MemoryWarn, MemoryAvailable: true,
		ThermalAvailable: true, ThermalState: 1,
		LoadAvailable: true, NumCPU: 8, Load1: 1,
		IdleAvailable: true, IdlePct: 60,
		WSScanOK: true, WSEvent: now.Add(-time.Hour),
		BootAvailable: true, BootTime: now.Add(-24 * time.Hour),
		VMActivityAvailable: true, SwapInAvailable: true, SwapOutAvailable: true,
		SwapInBytes: 4096, SwapOutBytes: 8192,
	}
	hist := History{
		LastSampleAt:         now.Add(-PrebuiltSafetyWindow),
		PrebuiltSwapAt:       now.Add(-PrebuiltSafetyWindow),
		PrebuiltSwapInBytes:  snap.SwapInBytes,
		PrebuiltSwapOutBytes: snap.SwapOutBytes,
	}
	d := EvaluatePrebuiltPressure(snap, PrebuiltThresholds(), hist, now)
	if !d.Admit {
		t.Fatalf("safe warning sample deferred: %v", d.Reasons)
	}

	for _, tc := range []struct {
		name string
		edit func(*PressureSnapshot, *History)
		want string
	}{
		{"short window", func(_ *PressureSnapshot, h *History) {
			h.LastSampleAt = now.Add(-29 * time.Second)
			h.PrebuiltSwapAt = h.LastSampleAt
		}, "30 seconds"},
		{"stale window", func(_ *PressureSnapshot, h *History) {
			h.LastSampleAt = now.Add(-46 * time.Second)
			h.PrebuiltSwapAt = h.LastSampleAt
		}, "fresh samples"},
		{"missing vm activity", func(s *PressureSnapshot, _ *History) { s.VMActivityAvailable = false }, "VM swap activity"},
		{"active swap out", func(s *PressureSnapshot, _ *History) { s.SwapOutBytes++ }, "swap activity"},
		{"active swap in", func(s *PressureSnapshot, _ *History) { s.SwapInBytes++ }, "swap activity"},
		{"critical memory", func(s *PressureSnapshot, _ *History) { s.MemoryLevel = MemoryCritical }, "memory pressure critical"},
		{"unknown memory", func(s *PressureSnapshot, _ *History) { s.MemoryLevel = "unknown" }, "memory pressure level"},
		{"serious thermal", func(s *PressureSnapshot, _ *History) { s.ThermalState = 2 }, "thermal"},
		{"low idle", func(s *PressureSnapshot, _ *History) { s.IdlePct = 29 }, "cpu saturation"},
		{"high load", func(s *PressureSnapshot, _ *History) { s.Load1 = 5.6 }, "cpu saturation"},
		{"missing load", func(s *PressureSnapshot, _ *History) { s.LoadAvailable = false }, "telemetry unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copySnap, copyHist := snap, hist
			tc.edit(&copySnap, &copyHist)
			got := EvaluatePrebuiltPressure(copySnap, PrebuiltThresholds(), copyHist, now)
			if got.Admit || !strings.Contains(strings.Join(got.Reasons, "; "), tc.want) {
				t.Fatalf("decision = %+v, want defer containing %q", got, tc.want)
			}
		})
	}
}

func TestPrebuiltQuietWindowUsesFreshSamplesAndRestartsAfterSwap(t *testing.T) {
	now := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	snap := PressureSnapshot{
		Platform: "darwin", MemoryLevel: MemoryWarn, MemoryAvailable: true,
		ThermalAvailable: true, ThermalState: 1,
		LoadAvailable: true, NumCPU: 8, Load1: 1,
		IdleAvailable: true, IdlePct: 60,
		WSScanOK: true, WSEvent: now.Add(-time.Hour),
		BootAvailable: true, BootTime: now.Add(-24 * time.Hour),
		VMActivityAvailable: true, SwapInAvailable: true, SwapOutAvailable: true,
		SwapInBytes: 4096, SwapOutBytes: 8192,
	}
	first := EvaluatePrebuiltPressure(snap, PrebuiltThresholds(), History{}, now)
	if first.Admit || first.Next.PrebuiltSwapAt != now {
		t.Fatalf("first safety sample = %+v", first)
	}
	second := EvaluatePrebuiltPressure(snap, PrebuiltThresholds(), first.Next, now.Add(15*time.Second))
	if second.Admit || second.RetryAfter != 15*time.Second {
		t.Fatalf("second 15-second sample = %+v", second)
	}
	third := EvaluatePrebuiltPressure(snap, PrebuiltThresholds(), second.Next, now.Add(30*time.Second))
	if !third.Admit {
		t.Fatalf("third sample did not complete the 30-second window: %+v", third)
	}

	changed := snap
	changed.SwapOutBytes++
	reset := EvaluatePrebuiltPressure(changed, PrebuiltThresholds(), first.Next, now.Add(15*time.Second))
	if reset.Admit || reset.Next.PrebuiltSwapAt != now.Add(15*time.Second) {
		t.Fatalf("swap activity did not restart the safety window: %+v", reset)
	}
	tooSoon := EvaluatePrebuiltPressure(changed, PrebuiltThresholds(), reset.Next, now.Add(30*time.Second))
	if tooSoon.Admit {
		t.Fatal("admitted 15 seconds after swap activity")
	}
	settled := EvaluatePrebuiltPressure(changed, PrebuiltThresholds(), tooSoon.Next, now.Add(45*time.Second))
	if !settled.Admit {
		t.Fatalf("did not admit after 30 quiet seconds: %+v", settled)
	}
}

func TestPrebuiltHardBlockerClearsSwapQuietWindow(t *testing.T) {
	now := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	snap := PressureSnapshot{
		Platform: "darwin", MemoryLevel: MemoryNormal, MemoryAvailable: true,
		ThermalAvailable: true, ThermalState: 1,
		LoadAvailable: true, NumCPU: 8, Load1: 1,
		IdleAvailable: true, IdlePct: 60,
		WSScanOK: true, WSEvent: now.Add(-time.Hour),
		BootAvailable: true, BootTime: now.Add(-24 * time.Hour),
		VMActivityAvailable: true, SwapInAvailable: true, SwapOutAvailable: true,
		SwapInBytes: 4096, SwapOutBytes: 8192,
	}
	hist := History{LastSampleAt: now, PrebuiltSwapAt: now, PrebuiltSwapInBytes: snap.SwapInBytes, PrebuiltSwapOutBytes: snap.SwapOutBytes}
	blocked := snap
	blocked.ThermalState = 2
	d := EvaluatePrebuiltPressure(blocked, PrebuiltThresholds(), hist, now.Add(15*time.Second))
	if d.Admit || len(d.Reasons) == 0 || !d.Next.PrebuiltSwapAt.IsZero() {
		t.Fatalf("hard blocker did not defer and reset window: %+v", d)
	}
	firstFresh := EvaluatePrebuiltPressure(snap, PrebuiltThresholds(), d.Next, now.Add(30*time.Second))
	if firstFresh.Admit || firstFresh.Next.PrebuiltSwapAt != now.Add(30*time.Second) {
		t.Fatalf("hard blocker did not restart the 30-second quiet window: %+v", firstFresh)
	}
	tooSoon := EvaluatePrebuiltPressure(snap, PrebuiltThresholds(), firstFresh.Next, now.Add(45*time.Second))
	if tooSoon.Admit {
		t.Fatal("hard blocker was followed by less than 30 seconds of healthy samples")
	}
	windowComplete := EvaluatePrebuiltPressure(snap, PrebuiltThresholds(), tooSoon.Next, now.Add(60*time.Second))
	if !windowComplete.Admit {
		t.Fatalf("hard blocker required more than the fresh 30-second quiet window: %+v", windowComplete)
	}
}

func TestParseDarwinSwapStats(t *testing.T) {
	input := `Mach Virtual Memory Statistics: (page size of 4096 bytes)
Swapins: 12.
Swapouts: 3.`
	in, out, ok := parseDarwinSwapStats(input)
	if !ok || in != 12*4096 || out != 3*4096 {
		t.Fatalf("parseDarwinSwapStats = %d, %d, %t", in, out, ok)
	}
	if _, _, ok := parseDarwinSwapStats("Swapins: 0.\nSwapouts: unknown"); ok {
		t.Fatal("accepted incomplete VM activity data")
	}
}
