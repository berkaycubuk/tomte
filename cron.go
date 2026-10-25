package tomte

// Schedules. A schedule is a five-field cron expression (minute, hour,
// day of month, month, day of week) in the machine's local time zone, a
// shortcut (@hourly, @daily, @weekly, @monthly, @yearly) or "@every 30m".
// Fields take *, numbers, ranges (1-5), lists (1,15), steps (*/10, 9-17/2)
// and, for months and weekdays, names (jan, mon). As in cron, when both
// day fields are restricted a day matching either one fires.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type schedule struct {
	spec, task string                                // task is a job's name for a job
	job        func(context.Context) (string, error) // nil for the model's tasks
	every      time.Duration                         // for @every; the fields are unused then

	minute, hour, dom, month, dow uint64 // bit i set: value i matches
	domStar, dowStar              bool
}

// Cron runs task on a schedule while the agent is serving. An agent can
// have several.
func Cron(spec, task string) Option {
	return func(a *Agent) {
		s, err := parseSchedule(spec)
		if err != nil {
			a.errs = append(a.errs, fmt.Errorf("cron %q: %w", spec, err))
			return
		}
		s.task = task
		a.schedules = append(a.schedules, s)
	}
}

var shortcuts = map[string]string{
	"@hourly":  "0 * * * *",
	"@daily":   "0 0 * * *",
	"@weekly":  "0 0 * * 0",
	"@monthly": "0 0 1 * *",
	"@yearly":  "0 0 1 1 *",
}

var (
	monthNames = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	dayNames   = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

func parseSchedule(spec string) (*schedule, error) {
	s := &schedule{spec: spec}
	expr := strings.TrimSpace(spec)
	if d, ok := strings.CutPrefix(expr, "@every "); ok {
		every, err := time.ParseDuration(strings.TrimSpace(d))
		if err != nil {
			return nil, err
		}
		if every < time.Second {
			return nil, errors.New("@every needs at least 1s")
		}
		s.every = every
		return s, nil
	}
	if long, ok := shortcuts[expr]; ok {
		expr = long
	}
	f := strings.Fields(expr)
	if len(f) != 5 {
		return nil, errors.New("want five fields: minute hour day-of-month month day-of-week")
	}
	var err error
	if s.minute, err = parseField(f[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if s.hour, err = parseField(f[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if s.dom, err = parseField(f[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if s.month, err = parseField(f[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if s.dow, err = parseField(f[4], 0, 7, dayNames); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	if s.dow&(1<<7) != 0 { // 7 is Sunday too
		s.dow |= 1
	}
	s.domStar, s.dowStar = f[2] == "*" || f[2] == "?", f[4] == "*" || f[4] == "?"
	if s.next(time.Now()).IsZero() {
		return nil, errors.New("never fires")
	}
	return s, nil
}

// parseField reads one field into a bit set. names, when given, name the
// values from min on.
func parseField(field string, min, max int, names []string) (uint64, error) {
	var bits uint64
	for part := range strings.SplitSeq(field, ",") {
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("bad step %q", stepStr)
			}
			step = n
		}
		lo, hi := min, max
		switch {
		case rng == "*" || rng == "?":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if lo, err = fieldValue(a, min, max, names); err != nil {
				return 0, err
			}
			if hi, err = fieldValue(b, min, max, names); err != nil {
				return 0, err
			}
			if lo > hi {
				return 0, fmt.Errorf("range %q goes backwards", rng)
			}
		default:
			v, err := fieldValue(rng, min, max, names)
			if err != nil {
				return 0, err
			}
			lo, hi = v, v
			if hasStep { // "5/15" means from 5 to the end, every 15
				hi = max
			}
		}
		for v := lo; v <= hi; v += step {
			bits |= 1 << v
		}
	}
	return bits, nil
}

func fieldValue(s string, min, max int, names []string) (int, error) {
	for i, n := range names {
		if strings.EqualFold(s, n) {
			return min + i, nil
		}
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("%d is outside %d-%d", v, min, max)
	}
	return v, nil
}

// next is the first time after t the schedule fires, or zero if it
// doesn't within five years (as for 30 February).
func (s *schedule) next(t time.Time) time.Time {
	if s.every > 0 {
		return t.Add(s.every)
	}
	loc := t.Location()
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		y, mo, d := t.Date()
		switch {
		case s.month&(1<<uint(mo)) == 0:
			t = time.Date(y, mo+1, 1, 0, 0, 0, 0, loc)
		case !s.dayMatches(t):
			t = time.Date(y, mo, d+1, 0, 0, 0, 0, loc)
		case s.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(y, mo, d, t.Hour()+1, 0, 0, 0, loc)
		case s.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

func (s *schedule) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<uint(t.Day())) != 0
	dow := s.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dow
	case s.dowStar:
		return dom
	}
	return dom || dow
}
