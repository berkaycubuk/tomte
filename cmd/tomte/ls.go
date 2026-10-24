package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// agentInfo is what `<agent> info` prints (info.go in tomte).
type agentInfo struct {
	Name      string `json:"name"`
	Model     string `json:"model"`
	Schedules []struct {
		Spec string    `json:"spec"`
		Task string    `json:"task"`
		Next time.Time `json:"next"`
	} `json:"schedules"`
	MaxDay float64  `json:"max_cost_day"`
	Errors []string `json:"errors"`
}

// runRecord is a line of an agent's run log (runlog.go in tomte).
type runRecord struct {
	Start   time.Time `json:"start"`
	Took    string    `json:"took"`
	Trigger string    `json:"trigger"`
	Task    string    `json:"task"`
	Steps   int       `json:"steps"`
	Tools   []string  `json:"tools"`
	Cost    float64   `json:"cost"`
	Result  string    `json:"result"`
	Error   string    `json:"error"`
}

// agentStatus is one agent as `tomte-helper ls` describes it.
type agentStatus struct {
	name  string
	unit  map[string]string // systemctl show
	info  *agentInfo        // nil when the binary didn't say
	today []runRecord
	last  *runRecord
}

func ls(args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	pos, err := parse(fs, args, 1, false, "<box>")
	if err != nil {
		return err
	}
	var out bytes.Buffer
	if err := box(pos[0]).helper(nil, &out, "ls"); err != nil {
		return err
	}
	now, agents, err := parseList(&out)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		fmt.Printf("no agents on %s yet; add one with `tomte deploy %s <package>`\n", pos[0], pos[0])
		return nil
	}
	printList(os.Stdout, now, agents)
	return nil
}

// parseList reads what `tomte-helper ls` prints: "now <unix time>", then
// for each agent an "agent <name>" line followed by "show", "info",
// "today" and "last" lines.
func parseList(r io.Reader) (now time.Time, agents []*agentStatus, err error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var a *agentStatus
	for sc.Scan() {
		kind, rest, _ := strings.Cut(sc.Text(), " ")
		switch {
		case kind == "now":
			sec, err := strconv.ParseInt(rest, 10, 64)
			if err != nil {
				return now, nil, fmt.Errorf("the box's time %q: %w", rest, err)
			}
			now = time.Unix(sec, 0)
		case kind == "agent":
			a = &agentStatus{name: rest, unit: map[string]string{}}
			agents = append(agents, a)
		case a == nil:
		case kind == "show":
			k, v, _ := strings.Cut(rest, "=")
			a.unit[k] = v
		case kind == "info":
			var in agentInfo
			if json.Unmarshal([]byte(rest), &in) == nil {
				a.info = &in
			}
		case kind == "today", kind == "last":
			var rec runRecord
			if json.Unmarshal([]byte(rest), &rec) != nil {
				continue
			}
			if kind == "today" {
				a.today = append(a.today, rec)
			} else {
				a.last = &rec
			}
		}
	}
	return now, agents, sc.Err()
}

func printList(w io.Writer, now time.Time, agents []*agentStatus) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tSTATE\tMEMORY\tNEXT RUN\tLAST RUN\tTODAY")
	for _, a := range agents {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", a.name, a.state(now), a.memory(), a.nextRun(now), a.lastRun(now), a.spent())
	}
	tw.Flush()
}

func (a *agentStatus) state(now time.Time) string {
	s := a.unit["ActiveState"]
	if s == "" {
		return "?"
	}
	if s != "active" {
		if sub := a.unit["SubState"]; sub != "" && sub != "dead" && sub != s {
			s += " (" + sub + ")"
		}
		return s
	}
	if t, ok := strings.CutPrefix(a.unit["ActiveEnterTimestamp"], "@"); ok {
		if sec, err := strconv.ParseInt(t, 10, 64); err == nil {
			s += " " + short(now.Sub(time.Unix(sec, 0)))
		}
	}
	return s
}

func (a *agentStatus) memory() string {
	n, err := strconv.ParseUint(a.unit["MemoryCurrent"], 10, 64)
	if err != nil || n == 1<<64-1 { // [not set], or not running
		return "-"
	}
	return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
}

func (a *agentStatus) nextRun(now time.Time) string {
	if a.info == nil {
		return "?"
	}
	t := a.info.next()
	switch {
	case t.IsZero():
		return "-"
	case t.Sub(now) < 12*time.Hour:
		return "in " + short(t.Sub(now))
	}
	return t.Format("Mon 15:04")
}

func (a *agentStatus) lastRun(now time.Time) string {
	if a.last == nil {
		return "-"
	}
	how := "sent"
	switch {
	case a.last.Error != "":
		how = "failed"
	case a.last.Result == "NOTHING":
		how = "nothing to say"
	}
	return short(now.Sub(a.last.Start)) + " ago, " + how
}

func (a *agentStatus) spent() string {
	var total float64
	for _, r := range a.today {
		total += r.Cost
	}
	runs := fmt.Sprintf("%d runs", len(a.today))
	if len(a.today) == 1 {
		runs = "1 run"
	}
	s := fmt.Sprintf("%s, $%.3f", runs, total)
	if a.info != nil && a.info.MaxDay > 0 {
		s += fmt.Sprintf(" of $%.2f", a.info.MaxDay)
	}
	return s
}

// short is a duration to the nearest sensible unit: 40s, 12m, 3h, 2d.
func short(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
