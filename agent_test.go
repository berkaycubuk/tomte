package tomte

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fakeModel serves the chat completions API with scripted replies and
// keeps the requests it got.
type fakeModel struct {
	mu       sync.Mutex
	replies  []string // assistant messages as JSON, one per request
	always   string   // the reply once replies run out, if set
	requests []chatRequest
}

func (f *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	f.requests = append(f.requests, req)
	if len(f.replies) == 0 && f.always != "" {
		f.replies = []string{f.always}
	}
	if len(f.replies) == 0 {
		http.Error(w, `{"error":{"message":"no more replies"}}`, 400)
		return
	}
	reply := f.replies[0]
	f.replies = f.replies[1:]
	io.WriteString(w, `{"choices":[{"message":`+reply+`,"finish_reason":"stop"}],"usage":{"prompt_tokens":100,"completion_tokens":10}}`)
}

func newFake(t *testing.T, replies ...string) (*fakeModel, Option) {
	f := &fakeModel{replies: replies}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	logPath := filepath.Join(t.TempDir(), "runs.jsonl")
	return f, func(a *Agent) {
		Model(srv.URL, "fake", "key")(a)
		Log(logPath)(a)
	}
}

func callReply(id, name, args string) string {
	b, _ := json.Marshal(args)
	return `{"role":"assistant","content":"","tool_calls":[{"id":"` + id + `","type":"function","function":{"name":"` + name + `","arguments":` + string(b) + `}}]}`
}

func textReply(s string) string {
	b, _ := json.Marshal(s)
	return `{"role":"assistant","content":` + string(b) + `}`
}

type addArgs struct {
	A int `json:"a"`
	B int `json:"b"`
}

func add(_ context.Context, in addArgs) (int, error) { return in.A + in.B, nil }

func TestDoCallsToolsAndReplies(t *testing.T) {
	f, m := newFake(t, callReply("c1", "add", `{"a":2,"b":3}`), textReply(" It's 5. "))
	a := New("adder", m, Instructions("Add numbers."), Tool("add", "Add two numbers", add))

	res, err := a.Do(context.Background(), "what is 2+3?")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "It's 5." || res.Steps != 2 || strings.Join(res.ToolCalls, ",") != "add" {
		t.Errorf("result = %+v", res)
	}
	if res.Usage != (Usage{PromptTokens: 200, CompletionTokens: 20}) {
		t.Errorf("usage = %+v", res.Usage)
	}

	first := f.requests[0]
	if first.Model != "fake" || len(first.Tools) != 1 || first.Tools[0].Function.Name != "add" {
		t.Errorf("first request = %+v", first)
	}
	if sys := first.Messages[0].Content; !strings.Contains(sys, "adder") || !strings.Contains(sys, "Add numbers.") {
		t.Errorf("system prompt = %q", sys)
	}
	second := f.requests[1].Messages
	last := second[len(second)-1]
	if last.Role != "tool" || last.ToolCallID != "c1" || last.Content != "5" {
		t.Errorf("tool result = %+v", last)
	}
}

func TestToolFailuresGoBackToTheModel(t *testing.T) {
	boom := func(context.Context, struct{}) (string, error) { panic("boom") }
	fail := func(context.Context, struct{}) (string, error) { return "", errors.New("no invoices") }
	f, m := newFake(t,
		callReply("c1", "boom", `{}`),
		callReply("c2", "fail", ``),
		callReply("c3", "missing", `{}`),
		callReply("c4", "add", `{"a":"x"}`),
		textReply("gave up"),
	)
	a := New("t", m, Tool("boom", "", boom), Tool("fail", "", fail), Tool("add", "", add))
	if _, err := a.Do(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	var results []string
	for _, msg := range f.requests[4].Messages {
		if msg.Role == "tool" {
			results = append(results, msg.Content)
		}
	}
	want := []string{"error: tool panicked: boom", "error: no invoices", `error: there is no tool named "missing"`, "error: invalid arguments"}
	if len(results) != len(want) {
		t.Fatalf("tool results = %q", results)
	}
	for i := range want {
		if !strings.HasPrefix(results[i], want[i]) {
			t.Errorf("result %d = %q, want prefix %q", i, results[i], want[i])
		}
	}
}

func TestStepLimit(t *testing.T) {
	_, m := newFake(t, callReply("c1", "add", `{}`), callReply("c2", "add", `{}`), textReply("late"))
	a := New("t", m, Tool("add", "", add), MaxSteps(2))
	res, err := a.Do(context.Background(), "go")
	if !errors.Is(err, ErrStepLimit) || res.Steps != 2 {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestOptionMistakes(t *testing.T) {
	a := New("t", Tool("add", "", add), Tool("add", "", add))
	_, err := a.Do(context.Background(), "go")
	if err == nil || !strings.Contains(err.Error(), "no model") || !strings.Contains(err.Error(), "declared twice") {
		t.Errorf("err = %v", err)
	}
	noop := func(context.Context) (string, error) { return "", nil }
	for want, opts := range map[string][]Option{
		"MaxSteps needs at least 1":   {Model("http://x", "m", ""), MaxSteps(0)},
		"job sweep is declared twice": {Job("@daily", "sweep", noop), Job("@hourly", "sweep", noop)},
	} {
		if err := errors.Join(New("t", opts...).errs...); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q, got %v", want, err)
		}
	}
}

func TestLongResultsAreCutBetweenCharacters(t *testing.T) {
	s := strings.Repeat("ü", maxResult) // two bytes each
	got := truncate("a" + s)            // so maxResult falls inside one
	head, _, _ := strings.Cut(got, "\n[cut off")
	if !utf8.ValidString(got) || len(head) != maxResult-1 || !strings.HasSuffix(got, fmt.Sprintf("%d more bytes]", 1+2*maxResult-len(head))) {
		t.Errorf("cut to %d bytes, valid %v: %q", len(head), utf8.ValidString(got), got[len(got)-40:])
	}
	if got := snippet([]byte(strings.Repeat("ü", 200))); !utf8.ValidString(got) {
		t.Errorf("snippet isn't valid UTF-8: %q", got)
	}
}

func TestToolInputMustBeAStruct(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	Tool("n", "", func(context.Context, int) (int, error) { return 0, nil })
}

func TestRunOnceSendsFailures(t *testing.T) {
	_, m := newFake(t) // no replies: the model call fails
	var out bytes.Buffer
	a := New("chaser", m, SendTo(writerChannel{&out}))
	if code := a.main(context.Background(), []string{"run", "chase", "invoices"}); code != 1 {
		t.Errorf("exit code = %d", code)
	}
	if !strings.HasPrefix(out.String(), "chaser failed after 1 step: model: 400") {
		t.Errorf("sent %q", out.String())
	}
}

func TestRunOnceSendsTheReply(t *testing.T) {
	f, m := newFake(t, textReply("done"))
	var out bytes.Buffer
	a := New("chaser", m, SendTo(writerChannel{&out}))
	if code := a.main(context.Background(), []string{"run", "chase", "invoices"}); code != 0 {
		t.Errorf("exit code = %d", code)
	}
	if out.String() != "done\n" {
		t.Errorf("sent %q", out.String())
	}
	if task := f.requests[0].Messages[1].Content; task != "chase invoices" {
		t.Errorf("task = %q", task)
	}
}

type failingChannel struct{}

func (failingChannel) Name() string                        { return "broken" }
func (failingChannel) Send(context.Context, Message) error { return errors.New("down") }

func TestSendToSeveralChannels(t *testing.T) {
	_, m := newFake(t, textReply("done"))
	var one, two bytes.Buffer
	a := New("chaser", m, SendTo(writerChannel{&one}, failingChannel{}, writerChannel{&two}))
	if name := a.channel.Name(); name != "stdout, broken, stdout" {
		t.Errorf("name = %q", name)
	}
	// One channel failing doesn't stop the others, but the run says so.
	if code := a.main(context.Background(), []string{"run", "chase"}); code != 1 {
		t.Errorf("exit code = %d", code)
	}
	if one.String() != "done\n" || two.String() != "done\n" {
		t.Errorf("sent %q and %q", one.String(), two.String())
	}
}
