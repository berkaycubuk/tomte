package tomte

import (
	"strings"
	"testing"
	"time"
)

func TestScheduleNext(t *testing.T) {
	ist := time.FixedZone("TRT", 3*3600)
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, ist)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct{ spec, from, want string }{
		{"* * * * *", "2026-10-23 18:04", "2026-10-23 18:05"},
		{"0 9 * * 1-5", "2026-10-23 18:04", "2026-10-26 09:00"}, // Friday evening → Monday
		{"0 9 * * mon-fri", "2026-10-26 08:59", "2026-10-26 09:00"},
		{"*/15 * * * *", "2026-10-23 18:04", "2026-10-23 18:15"},
		{"5/20 * * * *", "2026-10-23 18:30", "2026-10-23 18:45"},
		{"0 9-17/4 * * *", "2026-10-23 13:00", "2026-10-23 17:00"},
		{"30 8 1,15 * *", "2026-10-15 09:00", "2026-11-01 08:30"},
		{"0 0 31 * *", "2026-11-01 00:00", "2026-12-31 00:00"}, // November has no 31st
		{"0 0 29 feb *", "2026-03-01 00:00", "2028-02-29 00:00"},
		{"0 12 1 * sun", "2026-10-23 18:04", "2026-10-25 12:00"}, // either day field
		{"0 0 * * 7", "2026-10-23 18:04", "2026-10-25 00:00"},    // 7 is Sunday
		{"@daily", "2026-12-31 23:59", "2027-01-01 00:00"},
		{"@hourly", "2026-10-23 18:00", "2026-10-23 19:00"},
	}
	for _, tt := range tests {
		s, err := parseSchedule(tt.spec)
		if err != nil {
			t.Errorf("%q: %v", tt.spec, err)
			continue
		}
		if got := s.next(at(tt.from)); !got.Equal(at(tt.want)) {
			t.Errorf("%q after %s = %s, want %s", tt.spec, tt.from, got.Format("2006-01-02 15:04 Mon"), tt.want)
		}
	}
}

func TestScheduleEvery(t *testing.T) {
	s, err := parseSchedule("@every 90m")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if got := s.next(now); got.Sub(now) != 90*time.Minute {
		t.Errorf("next = %s", got.Sub(now))
	}
}

func TestScheduleErrors(t *testing.T) {
	for spec, want := range map[string]string{
		"* * * *":      "five fields",
		"60 * * * *":   "minute: 60 is outside 0-59",
		"* * * foo *":  `month: bad value "foo"`,
		"5-1 * * * *":  "goes backwards",
		"*/0 * * * *":  "bad step",
		"0 0 30 feb *": "never fires",
		"@every 10ms":  "at least 1s",
		"@every soon":  "invalid duration",
	} {
		_, err := parseSchedule(spec)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", spec, err, want)
		}
	}
}

func TestCronOptionReportsMistakes(t *testing.T) {
	_, m := newFake(t)
	a := New("t", m, Cron("bad", "x"))
	if len(a.errs) != 1 || !strings.Contains(a.errs[0].Error(), `cron "bad"`) {
		t.Errorf("errs = %v", a.errs)
	}
}

func TestDSTSpringForward(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip(err)
	}
	s, _ := parseSchedule("30 2 * * *") // 02:30 doesn't exist on 2026-03-29
	from := time.Date(2026, 3, 28, 12, 0, 0, 0, berlin)
	got := s.next(from)
	if want := time.Date(2026, 3, 30, 2, 30, 0, 0, berlin); !got.Equal(want) {
		t.Errorf("next = %s, want %s", got, want)
	}
}
