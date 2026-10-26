package tomte

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The tool, job and command below ignore their context, as code calling
// http.Get does, and wait until the test ends.

func TestTimeoutStopsAHungTool(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	stuck := func(context.Context, struct{}) (string, error) { <-release; return "late", nil }
	_, m := newFake(t, callReply("c1", "stuck", `{}`), textReply("done"))
	a := New("hung", m, Tool("stuck", "Never returns", stuck), Timeout(50*time.Millisecond))

	start := time.Now()
	res, err := a.Do(context.Background(), "go")
	if !errors.Is(err, ErrTimeLimit) || time.Since(start) > 2*time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
	if got := a.failure(res, err); !strings.Contains(got, "hung stopped after 1 step: time limit reached: 50ms") {
		t.Errorf("failure = %q", got)
	}
	if r := readLog(t, a); len(r) != 1 || !strings.Contains(r[0].Error, "time limit") {
		t.Errorf("log = %+v", r)
	}
}

func TestTimeoutStopsAHungJob(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	job := func(context.Context) (string, error) { <-release; return "late", nil }
	ch := &syncChannel{}
	a := New("hung", jobLog(t), SendTo(ch), Job("@daily", "stuck", job), Timeout(50*time.Millisecond))

	if code := a.main(context.Background(), []string{"run", "stuck"}); code != 1 {
		t.Errorf("exit code = %d", code)
	}
	if got := ch.messages(); len(got) != 1 || !strings.Contains(got[0], "time limit reached") {
		t.Errorf("sent %q", got)
	}
}

func TestTimeoutStopsAHungCommand(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	cmd := func(context.Context, Request) (string, error) { <-release; return "late", nil }
	a := New("hung", jobLog(t), Command("stuck", "", cmd), SendTo(&inbox{}), Timeout(50*time.Millisecond))

	_, err := a.runCommand(context.Background(), "test", a.command("stuck"), Request{}, "/stuck")
	if !errors.Is(err, ErrTimeLimit) {
		t.Errorf("err = %v", err)
	}
}

func TestTimeoutOptions(t *testing.T) {
	if a := New("x", Model("http://x", "m", "")); a.timeout != 10*time.Minute {
		t.Errorf("default timeout = %s", a.timeout)
	}
	if a := New("x", Model("http://x", "m", ""), Timeout(-time.Second)); len(a.errs) == 0 {
		t.Error("a negative timeout isn't a mistake")
	}
	a := New("x", Model("http://x", "m", ""), Timeout(0))
	ctx, cancel := a.limit(context.Background())
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Error("Timeout(0) still has a deadline")
	}
}
