package tomte

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Each fake reply costs 100 tokens in and 10 out: $0.11 at $1000 per
// million either way.
var dear = Price(1000, 1000)

func readLog(t *testing.T, a *Agent) []record {
	t.Helper()
	f, err := os.Open(a.logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rs []record
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var r record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}
		rs = append(rs, r)
	}
	return rs
}

func appendLog(t *testing.T, a *Agent, start time.Time, cost float64) {
	t.Helper()
	line, _ := json.Marshal(record{Start: start, Cost: cost})
	f, err := os.OpenFile(a.logPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Write(append(line, '\n'))
	f.Close()
}

func TestRunLog(t *testing.T) {
	_, m := newFake(t, callReply("c1", "add", `{"a":1,"b":1}`), textReply("2"))
	a := New("t", m, dear, Tool("add", "", add))
	if _, err := a.Do(context.Background(), "1+1"); err != nil {
		t.Fatal(err)
	}
	_, m2 := newFake(t)
	a.model = New("t", m2).model // the next run fails
	a.Do(context.Background(), "again")

	rs := readLog(t, a)
	if len(rs) != 2 {
		t.Fatalf("log = %+v", rs)
	}
	r := rs[0]
	if r.Trigger != "code" || r.Task != "1+1" || r.Steps != 2 || strings.Join(r.Tools, ",") != "add" ||
		r.TokensIn != 200 || r.TokensOut != 20 || r.Result != "2" || r.Error != "" || r.Start.IsZero() {
		t.Errorf("first record = %+v", r)
	}
	if r.Cost < 0.2199 || r.Cost > 0.2201 {
		t.Errorf("cost = %v", r.Cost)
	}
	if rs[1].Error == "" || rs[1].Result != "" {
		t.Errorf("second record = %+v", rs[1])
	}
}

func TestRunCostLimit(t *testing.T) {
	_, m := newFake(t, callReply("c1", "add", `{}`), callReply("c2", "add", `{}`), textReply("late"))
	a := New("t", m, dear, MaxCost(0.2, 0), Tool("add", "", add))
	res, err := a.Do(context.Background(), "go")
	if !errors.Is(err, ErrCostLimit) || res.Steps != 2 || len(res.ToolCalls) != 1 {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestReplyCrossingTheLimitIsKept(t *testing.T) {
	_, m := newFake(t, textReply("done"))
	a := New("t", m, dear, MaxCost(0.1, 0))
	res, err := a.Do(context.Background(), "go")
	if err != nil || res.Text != "done" {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestDailyLimitCountsTodaysLog(t *testing.T) {
	f, m := newFake(t, textReply("ok"))
	a := New("t", m, dear, MaxCost(0, 1))
	now := time.Now()
	appendLog(t, a, now.AddDate(0, 0, -1), 5) // yesterday doesn't count
	appendLog(t, a, now, 0.5)
	if _, err := a.Do(context.Background(), "go"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	appendLog(t, a, now, 0.5) // another process spent the rest
	res, err := a.Do(context.Background(), "go")
	if !errors.Is(err, ErrDailyLimit) || res.Steps != 0 || len(f.requests) != 1 {
		t.Errorf("res = %+v, err = %v, requests = %d", res, err, len(f.requests))
	}
	rs := readLog(t, a)
	if last := rs[len(rs)-1]; !strings.Contains(last.Error, "daily cost limit") {
		t.Errorf("refused run logged as %+v", last)
	}
}

func TestSpentOnReadsTheLogFromItsEnd(t *testing.T) {
	defer func(w int64) { spentWindow = w }(spentWindow)
	spentWindow = 100 // a line or so, so the window grows

	a := New("t", jobLog(t))
	day := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	midnight := time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)
	for i := range 50 {
		appendLog(t, a, midnight.AddDate(0, 0, -2).Add(time.Duration(i)*time.Minute), 1)
	}
	appendLog(t, a, midnight.Add(time.Hour), 0.25)
	appendLog(t, a, midnight.Add(2*time.Hour), 0.25)
	// A run that started yesterday and ended today is logged after today's.
	line, _ := json.Marshal(record{Start: midnight.Add(-time.Minute), Took: "3h0m0s", Cost: 7})
	f, _ := os.OpenFile(a.logPath, os.O_WRONLY|os.O_APPEND, 0)
	f.Write(append(line, '\n'))
	f.Close()
	appendLog(t, a, midnight.Add(3*time.Hour), 0.25)

	if got := a.spentOn(day); got != 0.75 {
		t.Errorf("spent %v, want 0.75", got)
	}
	if got := a.spentOn(day.AddDate(0, 0, -2)); got != 50 {
		t.Errorf("spent two days ago %v, want 50", got)
	}
}

func TestDailyLimitStopsARun(t *testing.T) {
	_, m := newFake(t, callReply("c1", "add", `{}`), callReply("c2", "add", `{}`), textReply("late"))
	a := New("t", m, dear, MaxCost(1, 0.3), Tool("add", "", add))
	appendLog(t, a, time.Now(), 0.1)
	res, err := a.Do(context.Background(), "go")
	if !errors.Is(err, ErrDailyLimit) || res.Steps != 2 {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestMaxCostNeedsPrice(t *testing.T) {
	_, m := newFake(t)
	_, err := New("t", m, MaxCost(1, 0)).Do(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "Price") {
		t.Errorf("err = %v", err)
	}
}

func TestRefusedRunSaysSo(t *testing.T) {
	_, m := newFake(t)
	var out strings.Builder
	a := New("chaser", m, dear, MaxCost(0, 1), SendTo(writerChannel{&out}))
	appendLog(t, a, time.Now(), 2)
	if code := a.main(context.Background(), []string{"run", "go"}); code != 1 {
		t.Errorf("exit code = %d", code)
	}
	if want := "chaser didn't run: daily cost limit reached: $2.00 of $1.00 today\n"; out.String() != want {
		t.Errorf("sent %q, want %q", out.String(), want)
	}
}

func TestConcurrentRunsShareTheDay(t *testing.T) {
	_, m := newFake(t, textReply("a"), textReply("b"), textReply("c"), textReply("d"))
	a := New("t", m, dear, MaxCost(0, 10))
	done := make(chan error)
	for range 4 {
		go func() {
			_, err := a.Do(context.Background(), "go")
			done <- err
		}()
	}
	for range 4 {
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
	if n := len(readLog(t, a)); n != 4 {
		t.Errorf("%d runs logged", n)
	}
	if got := a.spentOn(time.Now()); got < 0.4399 || got > 0.4401 {
		t.Errorf("spent today = %v", got)
	}
	if a.inflight > 1e-9 || a.inflight < -1e-9 {
		t.Errorf("inflight = %v after all runs ended", a.inflight)
	}
}
