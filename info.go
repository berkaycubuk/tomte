package tomte

// `./agent info` describes the agent as one line of JSON: its name,
// schedules and limits. The tomte command reads it to deploy an agent
// under its own name and to show when it runs next. It works with no
// environment and no network, and reports mistakes in the options
// instead of failing on them.

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type info struct {
	Name      string         `json:"name"`
	Model     string         `json:"model,omitempty"`
	Channel   string         `json:"channel"`
	Tools     []string       `json:"tools,omitempty"`
	Schedules []scheduleInfo `json:"schedules,omitempty"`
	MaxSteps  int            `json:"max_steps"`
	MaxRun    float64        `json:"max_cost_run,omitempty"`
	MaxDay    float64        `json:"max_cost_day,omitempty"`
	Errors    []string       `json:"errors,omitempty"`
}

type scheduleInfo struct {
	Spec string    `json:"spec"`
	Task string    `json:"task"`
	Next time.Time `json:"next"`
}

func (a *Agent) info() info {
	in := info{Name: a.name, Channel: a.channel.Name(), MaxSteps: a.maxSteps, MaxRun: a.maxRun, MaxDay: a.maxDay}
	if a.model != nil {
		in.Model = a.model.name
	}
	for _, s := range a.specs {
		in.Tools = append(in.Tools, s.Function.Name)
	}
	now := a.now()
	for _, s := range a.schedules {
		in.Schedules = append(in.Schedules, scheduleInfo{Spec: s.spec, Task: s.task, Next: s.next(now)})
	}
	for _, err := range a.errs {
		in.Errors = append(in.Errors, err.Error())
	}
	return in
}

func (a *Agent) printInfo(w io.Writer) int {
	line, err := json.Marshal(a.info())
	if err != nil {
		fmt.Fprintf(w, "%s: info: %v\n", a.name, err)
		return 1
	}
	fmt.Fprintf(w, "%s\n", line)
	return 0
}
