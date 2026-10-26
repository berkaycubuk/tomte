package tomte

// Jobs: Go code on a schedule, with no model. Some chores need no
// judgment, like noticing a login from somewhere new; a job does the
// work and says what it found, and an agent with only jobs needs no
// model. Its runs go to the channel and the run log like the model's.

import (
	"context"
	"fmt"
	"strings"
)

// Job runs fn on a schedule while the agent is serving, and from the
// command line with `run <name>`. What fn returns is sent through the
// channel unless it's empty or NOTHING; an error is sent as a failure.
func Job(spec, name string, fn func(context.Context) (string, error)) Option {
	return func(a *Agent) {
		s, err := parseSchedule(spec)
		if err != nil {
			a.errs = append(a.errs, fmt.Errorf("job %s: cron %q: %w", name, spec, err))
			return
		}
		if name == "" || strings.ContainsAny(name, " \t\n") {
			a.errs = append(a.errs, fmt.Errorf("job %q: name it with one word", name))
			return
		}
		s.task, s.job = name, fn
		a.schedules = append(a.schedules, s)
	}
}

// job is the job named name, or nil.
func (a *Agent) job(name string) *schedule {
	for _, s := range a.schedules {
		if s.job != nil && s.task == name {
			return s
		}
	}
	return nil
}

func (a *Agent) jobs() (names []string) {
	for _, s := range a.schedules {
		if s.job != nil {
			names = append(names, s.task)
		}
	}
	return names
}

// needsModel says whether anything but jobs and commands is declared: the
// model does everything else.
func (a *Agent) needsModel() bool {
	if len(a.tools) > 0 || len(a.jobs()) == 0 && len(a.commands) == 0 {
		return true
	}
	for _, s := range a.schedules {
		if s.job == nil {
			return true
		}
	}
	return false
}

func (a *Agent) runJob(ctx context.Context, trigger string, s *schedule) (text string, err error) {
	start := a.now()
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panicked: %v", r)
			}
		}()
		text, err = s.job(ctx)
	}()
	text = strings.TrimSpace(text)
	a.mu.Lock()
	a.logRun(start, trigger, s.task, Result{Text: text}, err)
	a.mu.Unlock()
	return text, err
}
