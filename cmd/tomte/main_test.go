package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSetupScript(t *testing.T) {
	script, err := setupFor("512M", "150%")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "setup.sh")
	os.WriteFile(path, []byte(script), 0o644)
	if out, err := exec.Command("sh", "-n", path).CombinedOutput(); err != nil {
		t.Fatalf("setup.sh: %v\n%s", err, out)
	}
	for _, want := range []string{"MemoryMax=512M", "CPUQuota=150%", "Slice=tomte.slice", "check_name()", "150% CPU"} {
		if !strings.Contains(script, want) {
			t.Errorf("setup.sh has no %q", want)
		}
	}
	// The files go in whole, through heredocs that end where they should.
	for _, f := range []string{unitFile, helperScript} {
		if !strings.Contains(script, f+"TOMTE_EOF\n") {
			t.Errorf("a file doesn't end its heredoc:\n%s", f[:80])
		}
	}
}

func TestPrintList(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	in := &agentInfo{Name: "digest", MaxDay: 0.25}
	in.Schedules = append(in.Schedules, struct {
		Spec string    `json:"spec"`
		Task string    `json:"task"`
		Next time.Time `json:"next"`
	}{Next: now.Add(90 * time.Minute)})
	agents := []*agentStatus{
		{
			name:  "digest",
			unit:  map[string]string{"ActiveState": "active", "MemoryCurrent": "11534336", "ActiveEnterTimestamp": "@" + strconv.FormatInt(now.Add(-3*time.Hour).Unix(), 10)},
			info:  in,
			today: []runRecord{{Cost: 0.004}, {Cost: 0.008}},
			last:  &runRecord{Start: now.Add(-2 * time.Hour), Result: "NOTHING"},
		},
		{
			name: "broken",
			unit: map[string]string{"ActiveState": "activating", "SubState": "auto-restart", "MemoryCurrent": "[not set]"},
		},
	}
	var out bytes.Buffer
	printList(&out, now, agents)
	want := `AGENT    STATE                       MEMORY   NEXT RUN   LAST RUN                 TODAY
digest   active 3h                   11.0M    in 1h      2h ago, nothing to say   2 runs, $0.012 of $0.25
broken   activating (auto-restart)   -        ?          -                        0 runs, $0.000
`
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestGoArch(t *testing.T) {
	for machine, want := range map[string]string{"x86_64": "amd64", "aarch64": "arm64", "armv7l": "arm7", "armv6l": "arm6"} {
		arch, arm, err := goArch(machine)
		if err != nil || arch+arm != want {
			t.Errorf("goArch(%q) = %s %s %v", machine, arch, arm, err)
		}
	}
	if _, _, err := goArch("pdp11"); err == nil {
		t.Error("pdp11 has a Go architecture")
	}
}

func TestQuote(t *testing.T) {
	if got := quote("a b", "it's"); got != `'a b' 'it'\''s'` {
		t.Errorf("quote = %s", got)
	}
}
