package tomte

// The run log: one JSON line per run, appended to a file, saying what
// started it, what it did, what it cost and how it ended. It's what to
// read when an agent did something odd, and where the daily cost limit
// counts the day's spending.

import (
	"bufio"
	"encoding/json"
	"errors"
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

// spentOn sums the cost of the logged runs that started on day's date,
// in day's time zone. Lines it can't read are skipped.
func (a *Agent) spentOn(day time.Time) float64 {
	f, err := os.Open(a.logPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("%s: run log: %v", a.name, err)
		}
		return 0
	}
	defer f.Close()
	y, m, d := day.Date()
	var total float64
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r struct {
			Start time.Time `json:"start"`
			Cost  float64   `json:"cost"`
		}
		if json.Unmarshal(sc.Bytes(), &r) != nil {
			continue
		}
		if ry, rm, rd := r.Start.In(day.Location()).Date(); ry == y && rm == m && rd == d {
			total += r.Cost
		}
	}
	return total
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
