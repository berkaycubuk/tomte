package main

import (
	"bytes"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	// Run as root on someone's machine, the scripts get the strictest
	// reading there is, where there's one.
	if _, err := exec.LookPath("shellcheck"); err == nil {
		helper := filepath.Join(t.TempDir(), "tomte-helper")
		os.WriteFile(helper, []byte(helperScript), 0o644)
		if out, err := exec.Command("shellcheck", "-s", "sh", path, helper).CombinedOutput(); err != nil {
			t.Errorf("shellcheck: %v\n%s", err, out)
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

// One-off runs must be as confined as the service.
func TestRunMatchesTheUnit(t *testing.T) {
	keys := []string{"Slice", "ProtectSystem", "ProtectHome", "PrivateTmp", "PrivateDevices", "NoNewPrivileges", "MemoryMax", "CPUQuota"}
	for line := range strings.Lines(unitFile) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && slices.Contains(keys, k) && !strings.Contains(helperScript, "-p "+k+"="+v+" ") {
			t.Errorf("tomte-helper run doesn't set %s=%s like the unit", k, v)
		}
	}
}

func TestParse(t *testing.T) {
	for _, c := range []struct {
		args []string
		n    int
		rest bool
		want string
		f    bool
	}{
		{[]string{"box", "agent"}, 2, false, "box agent", false},
		{[]string{"-f", "box", "agent"}, 2, false, "box agent", true},
		{[]string{"box", "agent", "-f"}, 2, false, "box agent", true},
		{[]string{"box", "-f", "agent"}, 2, false, "box agent", true},
		{[]string{"box", "agent", "say", "-f", "now"}, 2, true, "box agent say -f now", false},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		f := fs.Bool("f", false, "")
		pos, err := parse(fs, c.args, c.n, c.rest, "")
		if err != nil || strings.Join(pos, " ") != c.want || *f != c.f {
			t.Errorf("parse(%q) = %q, -f %v, %v", c.args, pos, *f, err)
		}
	}
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if _, err := parse(fs, []string{"box", "agent", "extra"}, 2, false, ""); err == nil {
		t.Error("an extra argument was taken")
	}
}

func TestHideValues(t *testing.T) {
	got := hideValues("# keys\nMODEL_KEY=secret\nEMPTY=\nTZ=Europe/Istanbul\n")
	if want := "# keys\nMODEL_KEY=…\nEMPTY=\nTZ=…\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPrintRuns(t *testing.T) {
	start := time.Date(2026, 10, 24, 19, 43, 0, 0, time.UTC)
	var out bytes.Buffer
	printRuns(&out, []runRecord{
		{Start: start, Trigger: "cli", Took: "30.5s", Steps: 2, Cost: 0.007, Result: "line one\nline two"},
		{Start: start, Trigger: "cron", Took: "1s", Steps: 1, Result: "NOTHING"},
		{Start: start, Trigger: "cron", Took: "2s", Steps: 1, Error: "429 " + strings.Repeat("x", 100)},
	})
	for _, want := range []string{"line one …", "nothing to say", "failed: 429 xxx", "$0.007", "Sat 24 Oct 19:43"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("no %q in\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), strings.Repeat("x", 80)) {
		t.Errorf("a long outcome wasn't cut:\n%s", out.String())
	}
}

func TestIsTerminal(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	f, _ := os.CreateTemp(t.TempDir(), "")
	defer f.Close()
	if isTerminal(null) || isTerminal(f) {
		t.Error("/dev/null or a file passed for a terminal")
	}
}
