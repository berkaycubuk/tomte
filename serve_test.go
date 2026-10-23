package tomte

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncChannel records what's sent, safely from several runs.
type syncChannel struct {
	mu   sync.Mutex
	sent []string
}

func (*syncChannel) Name() string { return "test" }

func (c *syncChannel) Send(_ context.Context, m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m.Text)
	return nil
}

func (c *syncChannel) messages() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

func every(d time.Duration, task string) Option {
	return func(a *Agent) {
		a.schedules = append(a.schedules, &schedule{spec: "every " + d.String(), task: task, every: d})
	}
}

func TestServeRunsSchedules(t *testing.T) {
	f, m := newFake(t, textReply("first"))
	f.always = textReply(nothing)
	ch := &syncChannel{}
	a := New("t", m, SendTo(ch), every(30*time.Millisecond, "check things"))

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if code := a.main(ctx, nil); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	rs := readLog(t, a)
	if len(rs) < 3 {
		t.Fatalf("%d runs logged", len(rs))
	}
	for _, r := range rs {
		if r.Trigger != "cron" || r.Task != "check things" {
			t.Errorf("record = %+v", r)
		}
	}
	// Only the run with something to say sent a message.
	if got := ch.messages(); len(got) != 1 || got[0] != "first" {
		t.Errorf("sent %q", got)
	}
	f.mu.Lock()
	sys := f.requests[0].Messages[0].Content
	f.mu.Unlock()
	if !strings.Contains(sys, "started by a schedule") {
		t.Errorf("system prompt = %q", sys)
	}
}

func TestServeSkipsWhileTheLastRunIsGoing(t *testing.T) {
	release := make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(1)
	slow := func(ctx context.Context, _ struct{}) (string, error) {
		calls.Done()
		<-release
		return "ok", nil
	}
	f, m := newFake(t, callReply("c1", "slow", `{}`), textReply("done"))
	ch := &syncChannel{}
	a := New("t", m, SendTo(ch), Tool("slow", "", slow), every(20*time.Millisecond, "go"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int)
	go func() { done <- a.main(ctx, []string{"serve"}) }()
	calls.Wait()
	time.Sleep(100 * time.Millisecond) // several turns come due meanwhile
	cancel()                           // stop scheduling; the run in progress may finish
	time.Sleep(20 * time.Millisecond)
	close(release)
	if code := <-done; code != 0 {
		t.Errorf("exit code = %d", code)
	}

	f.mu.Lock()
	n := len(f.requests)
	f.mu.Unlock()
	if n != 2 {
		t.Errorf("%d model calls, want 2 (one run)", n)
	}
	if got := ch.messages(); len(got) != 1 || got[0] != "done" {
		t.Errorf("sent %q", got)
	}
}

func TestServeNeedsASchedule(t *testing.T) {
	_, m := newFake(t)
	if code := New("t", m).main(context.Background(), nil); code != 2 {
		t.Errorf("exit code = %d", code)
	}
}
