package tomte

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// inbox is a channel people write to: it hands over its messages, then
// waits, and keeps what's sent with who it's to.
type inbox struct {
	in   []Incoming
	mu   sync.Mutex
	sent []Message
}

func (*inbox) Name() string { return "inbox" }

func (c *inbox) Send(_ context.Context, m Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return nil
}

func (c *inbox) Receive(ctx context.Context, handle func(Incoming)) error {
	for _, in := range c.in {
		handle(in)
	}
	<-ctx.Done()
	return nil
}

func (c *inbox) replies() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for _, m := range c.sent {
		out[m.To] = m.Text
	}
	return out
}

func TestCommands(t *testing.T) {
	report := func(_ context.Context, r Request) (string, error) {
		return "report on " + r.Args + " for " + r.From, nil
	}
	quiet := func(context.Context, Request) (string, error) { return nothing, nil }
	fail := func(context.Context, Request) (string, error) { return "", errors.New("db is locked") }
	boom := func(context.Context, Request) (string, error) { panic("oops") }
	ch := &inbox{in: []Incoming{
		{From: "1", Text: "/report  example.com "},
		{From: "2", Text: "/REPORT@SiteBot a.com"},
		{From: "3", Text: "/quiet"},
		{From: "4", Text: "/fail"},
		{From: "5", Text: "/boom"},
		{From: "6", Text: "/nope"},
		{From: "7", Text: "hello"},
		{From: "8", Text: "/start"},
		{From: "9", Text: "/secret"},
		{From: "10", Text: "/help"},
	}}
	a := New("site", jobLog(t), SendTo(ch),
		Command("/report", "Report on a site", report),
		Command("quiet", "", quiet),
		Command("fail", "", fail),
		Command("boom", "", boom),
		HiddenCommand("secret", "Owner only", func(context.Context, Request) (string, error) { return "found it", nil }),
		Owner("10"),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if code := a.main(ctx, nil); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	got := ch.replies()
	help := "Commands:\n/report: Report on a site\n/quiet\n/fail\n/boom\n/help: this list"
	want := map[string]string{
		"1":  "report on example.com for 1",
		"2":  "report on a.com for 2",
		"4":  "Sorry, /fail didn't work. Try again later.",
		"5":  "Sorry, /boom didn't work. Try again later.",
		"6":  "There's no /nope.\n\n" + help,
		"7":  help,
		"8":  help,
		"9":  "found it", // answered, though help leaves it out
		"10": help + "\n\nOnly yours:\n/secret: Owner only",
	}
	if len(got) != len(want) {
		t.Errorf("replies = %q", got)
	}
	for to, w := range want {
		if got[to] != w {
			t.Errorf("reply to %s = %q, want %q", to, got[to], w)
		}
	}
	for _, m := range ch.sent {
		if m.Agent != "" {
			t.Errorf("a reply is signed: %+v", m)
		}
	}

	// Commands are logged; help isn't.
	rs := readLog(t, a)
	if len(rs) != 6 {
		t.Fatalf("%d runs logged", len(rs))
	}
	errs := map[string]string{}
	for _, r := range rs {
		if r.Trigger != "inbox" {
			t.Errorf("record = %+v", r)
		}
		errs[r.Task] = r.Error
	}
	if errs["/fail"] != "db is locked" || errs["/boom"] != "panicked: oops" {
		t.Errorf("errors = %q", errs)
	}
}

func TestCommandMistakes(t *testing.T) {
	ok := func(context.Context, Request) (string, error) { return "", nil }
	tests := []struct {
		opts []Option
		want string
	}{
		{[]Option{Command("Report", "", ok), SendTo(&inbox{})}, "lowercase"},
		{[]Option{Command("a b", "", ok), SendTo(&inbox{})}, "lowercase"},
		{[]Option{Command("x", "", ok), Command("/x", "", ok), SendTo(&inbox{})}, "declared twice"},
		{[]Option{Command("x", "", ok)}, "people can write to"},
	}
	for _, tt := range tests {
		a := New("t", tt.opts...)
		if err := errors.Join(a.errs...); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("errs = %v, want %q", err, tt.want)
		}
	}
	// Commands need no model, and are what the agent serves.
	a := New("t", Command("x", "", ok), SendTo(&inbox{}))
	if len(a.errs) != 0 {
		t.Errorf("errs = %v", a.errs)
	}
	if in := a.info(); strings.Join(in.Commands, ",") != "x" {
		t.Errorf("info = %+v", in)
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct{ text, name, args string }{
		{"/report", "report", ""},
		{" /report\texample.com  more ", "report", "example.com  more"},
		{"/Report@MyBot x", "report", "x"},
		{"/report\nline two", "report", "line two"},
	}
	for _, tt := range tests {
		name, args, ok := parseCommand(tt.text)
		if !ok || name != tt.name || args != tt.args {
			t.Errorf("parseCommand(%q) = %q, %q, %v", tt.text, name, args, ok)
		}
	}
	if _, _, ok := parseCommand("report /x"); ok {
		t.Error("text that doesn't start with / is a command")
	}
}
