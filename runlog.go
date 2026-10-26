package tomte

// The run log: one JSON line per run, appended to a file, saying what
// started it, what it did, what it cost and how it ended. It's what to
// read when an agent did something odd, and where the daily cost limit
// counts the day's spending.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"time"
)

type record struct {
	Start     time.Time `json:"start"`
	Took      string    `json:"took"`
	Trigger   string    `json:"trigger"`
	Task      string    `json:"task"`
	Steps     int       `json:"steps"`
	Tools     []string  `json:"tools,omitempty"`
	TokensIn  int       `json:"tokens_in"`
	TokensOut int       `json:"tokens_out"`
	Cost      float64   `json:"cost"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// logRun appends the run to the log. Called with a.mu held; a log that
// can't be written is reported but doesn't fail the run.
func (a *Agent) logRun(start time.Time, trigger, task string, res Result, err error) {
	r := record{
		Start:     start,
		Took:      a.now().Sub(start).Round(100 * time.Millisecond).String(),
		Trigger:   trigger,
		Task:      cut(task, 500),
		Steps:     res.Steps,
		Tools:     res.ToolCalls,
		TokensIn:  res.Usage.PromptTokens,
		TokensOut: res.Usage.CompletionTokens,
		Cost:      res.Cost,
		Result:    cut(res.Text, 500),
	}
	if err != nil {
		r.Error = cut(err.Error(), 500)
	}
	line, _ := json.Marshal(r)
	f, ferr := os.OpenFile(a.logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if ferr == nil {
		_, ferr = f.Write(append(line, '\n'))
		if cerr := f.Close(); ferr == nil {
			ferr = cerr
		}
	}
	if ferr != nil {
		log.Printf("%s: run log: %v", a.name, ferr)
	}
}

// spentWindow is how much of the end of the log spentOn reads first.
var spentWindow int64 = 64 << 10

// spentOn sums the cost of the logged runs that started on day's date,
// in day's time zone. Runs are logged as they end, so the log is read
// from its end, in a window that doubles until it reaches a run that
// ended before the day began: every line before that one did too. Lines
// it can't read are skipped.
func (a *Agent) spentOn(day time.Time) float64 {
	f, err := os.Open(a.logPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("%s: run log: %v", a.name, err)
		}
		return 0
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		log.Printf("%s: run log: %v", a.name, err)
		return 0
	}
	y, m, d := day.Date()
	// A minute early, for clocks and the rounding of "took".
	before := time.Date(y, m, d, 0, 0, 0, 0, day.Location()).Add(-time.Minute)
	for window := spentWindow; ; window *= 2 {
		off := max(fi.Size()-window, 0)
		buf := make([]byte, fi.Size()-off)
		if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
			log.Printf("%s: run log: %v", a.name, err)
			return 0
		}
		lines := bytes.Split(buf, []byte("\n"))
		if off > 0 {
			lines = lines[1:] // cut by the window
		}
		var total float64
		done := off == 0
		for _, line := range lines {
			var r struct {
				Start time.Time `json:"start"`
				Took  string    `json:"took"`
				Cost  float64   `json:"cost"`
			}
			if json.Unmarshal(line, &r) != nil {
				continue
			}
			took, _ := time.ParseDuration(r.Took)
			if r.Start.Add(took).Before(before) {
				done = true
			}
			if ry, rm, rd := r.Start.In(day.Location()).Date(); ry == y && rm == m && rd == d {
				total += r.Cost
			}
		}
		if done {
			return total
		}
	}
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
