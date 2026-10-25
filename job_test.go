package tomte

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func jobLog(t *testing.T) Option {
	return Log(filepath.Join(t.TempDir(), "runs.jsonl"))
}

// everyJob is Job with a period shorter than @every allows.
func everyJob(d time.Duration, name string, fn func(context.Context) (string, error)) Option {
	return func(a *Agent) {
		a.schedules = append(a.schedules, &schedule{spec: "every " + d.String(), task: name, job: fn, every: d})
	}
}

func TestJobsNeedNoModel(t *testing.T) {
	n := 0
	count := func(context.Context) (string, error) {
		n++
		if n == 1 {
			return " found one \n", nil
		}
		return nothing, nil
	}
	ch := &syncChannel{}
	a := New("watch", jobLog(t), SendTo(ch), everyJob(30*time.Millisecond, "count", count))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if code := a.main(ctx, nil); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	rs := readLog(t, a)
	if len(rs) < 3 {
		t.Fatalf("%d runs logged", len(rs))
	}
	if r := rs[0]; r.Trigger != "cron" || r.Task != "count" || r.Steps != 0 || r.Result != "found one" {
		t.Errorf("record = %+v", r)
	}
	if got := ch.messages(); len(got) != 1 || got[0] != "found one" {
		t.Errorf("sent %q", got)
	}
}

func TestRunDoesAJob(t *testing.T) {
	fail := func(context.Context) (string, error) { return "", errors.New("no journal") }
	boom := func(context.Context) (string, error) { panic("oops") }
	ch := &syncChannel{}
	a := New("watch", jobLog(t), SendTo(ch), Job("@hourly", "fail", fail), Job("@hourly", "boom", boom))

	if code := a.main(context.Background(), []string{"run", "fail"}); code != 1 {
		t.Errorf("exit code = %d", code)
	}
	if code := a.main(context.Background(), []string{"run", "boom"}); code != 1 {
		t.Errorf("exit code = %d", code)
	}
	// Without a model, anything but a job's name is a mistake, said so.
	if code := a.main(context.Background(), []string{"run", "what's new?"}); code != 1 {
		t.Errorf("exit code = %d", code)
	}
	got := ch.messages()
	want := []string{"watch's job fail failed: no journal", "watch's job boom failed: panicked: oops", "no model"}
	if len(got) != len(want) {
		t.Fatalf("sent %q", got)
	}
	for i := range want {
		if !strings.Contains(got[i], want[i]) {
			t.Errorf("message %d = %q, want %q in it", i, got[i], want[i])
		}
	}
	if rs := readLog(t, a); len(rs) != 2 || rs[0].Trigger != "cli" || rs[0].Error != "no journal" {
		t.Errorf("log = %+v", rs)
	}
}

func TestAJobAndTheModel(t *testing.T) {
	_, m := newFake(t, textReply("from the model"))
	ch := &syncChannel{}
	job := func(context.Context) (string, error) { return "from the job", nil }
	a := New("both", m, SendTo(ch), Cron("@daily", "think"), Job("@hourly", "check", job))

	a.main(context.Background(), []string{"run", "check"})
	a.main(context.Background(), []string{"run", "think", "hard"})
	if got := ch.messages(); len(got) != 2 || got[0] != "from the job" || got[1] != "from the model" {
		t.Errorf("sent %q", got)
	}
}

func TestWhatNeedsAModel(t *testing.T) {
	job := func(context.Context) (string, error) { return "", nil }
	for _, c := range []struct {
		name string
		opts []Option
		ok   bool
	}{
		{"only jobs", []Option{Job("@hourly", "a", job), Job("@daily", "b", job)}, true},
		{"nothing", nil, false},
		{"a cron task", []Option{Job("@hourly", "a", job), Cron("@daily", "think")}, false},
		{"a tool", []Option{Job("@hourly", "a", job), Tool("add", "", add)}, false},
		{"a bad job name", []Option{Job("@hourly", "check logins", job)}, false},
	} {
		a := New("t", c.opts...)
		if ok := len(a.errs) == 0; ok != c.ok {
			t.Errorf("%s: errors %v", c.name, a.errs)
		}
	}
}

func TestInfoSaysJobsAndJournal(t *testing.T) {
	job := func(context.Context) (string, error) { return "", nil }
	var b bytes.Buffer
	New("watch", ReadsJournal(), Job("@hourly", "logins", job)).printInfo(&b)
	if !strings.Contains(b.String(), `"journal":true`) {
		t.Errorf("info = %s", b.String())
	}
	var in info
	if err := json.Unmarshal(b.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	if len(in.Schedules) != 1 || !in.Schedules[0].Job || in.Schedules[0].Task != "logins" || in.Model != "" || len(in.Errors) != 0 {
		t.Errorf("info = %+v", in)
	}
}
