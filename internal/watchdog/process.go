package watchdog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// PSArgs is the column set the reaper samples, kept in one place so the
// parser and the caller can never drift apart.
const PSArgs = "pid,ppid,pcpu,etime,args"

// Process is one row of `ps -Ao pid,ppid,pcpu,etime,args`.
type Process struct {
	PID   int
	PPID  int
	CPU   float64
	Etime time.Duration // how long the process has been running
	Args  string        // full command line
}

// StartKey identifies a process incarnation: the PID alone collides after
// PID reuse, so the derived start time (now - etime) disambiguates. Two runs
// of the same live process derive the same start second because etime has
// one-second resolution.
func (p Process) StartKey(now time.Time) string {
	return fmt.Sprintf("%d@%d", p.PID, now.Add(-p.Etime).Unix())
}

// Executable returns the first token of the command line.
func (p Process) Executable() string {
	fields := strings.Fields(p.Args)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// ParsePS parses `ps -Ao pid,ppid,pcpu,etime,args` output, header included.
func ParsePS(output string) ([]Process, error) {
	var procs []Process
	for i, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 && strings.HasPrefix(line, "PID") {
			continue
		}
		p, err := parsePSLine(line)
		if err != nil {
			return nil, fmt.Errorf("ps output line %d: %w", i+1, err)
		}
		procs = append(procs, p)
	}
	return procs, nil
}

func parsePSLine(line string) (Process, error) {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return Process{}, fmt.Errorf("expected at least 5 columns, got %d: %q", len(fields), line)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return Process{}, fmt.Errorf("bad pid %q", fields[0])
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return Process{}, fmt.Errorf("bad ppid %q", fields[1])
	}
	cpu, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return Process{}, fmt.Errorf("bad pcpu %q", fields[2])
	}
	etime, err := ParseEtime(fields[3])
	if err != nil {
		return Process{}, err
	}
	return Process{
		PID:   pid,
		PPID:  ppid,
		CPU:   cpu,
		Etime: etime,
		Args:  strings.Join(fields[4:], " "),
	}, nil
}

// ParseEtime parses ps ELAPSED values: mm:ss, hh:mm:ss, or dd-hh:mm:ss.
func ParseEtime(s string) (time.Duration, error) {
	var days int64
	if before, after, found := strings.Cut(s, "-"); found {
		d, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("bad etime %q", s)
		}
		days = d
		s = after
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, fmt.Errorf("bad etime %q", s)
	}
	nums := make([]int64, len(parts))
	for i, part := range parts {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("bad etime %q", s)
		}
		nums[i] = n
	}
	var hours, minutes, seconds int64
	if len(nums) == 3 {
		hours, minutes, seconds = nums[0], nums[1], nums[2]
	} else {
		minutes, seconds = nums[0], nums[1]
	}
	return time.Duration(days)*24*time.Hour +
		time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second, nil
}
