package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/berkaycubuk/tomte"
)

func TestToHTML(t *testing.T) {
	tests := []struct{ md, want string }{
		{"plain & <safe>", "plain &amp; &lt;safe&gt;"},
		{"## Overdue invoices", "<b>Overdue invoices</b>"},
		{"**3** sent, *2* skipped, ~~1~~", "<b>3</b> sent, <i>2</i> skipped, <s>1</s>"},
		{"- one\n  * two", "• one\n  • two"},
		{"run `rm *.tmp` now", "run <code>rm *.tmp</code> now"},
		{"see [the invoice](https://x.com/a?b=1&c=2)", `see <a href="https://x.com/a?b=1&amp;c=2">the invoice</a>`},
		{"[bad](javascript:alert(1))", "[bad](javascript:alert(1))"},
		{"snake_case_name and 2 * 3 * 4", "snake_case_name and 2 * 3 * 4"},
		{"```go\nif a < b {}\n```\nafter", "<pre>if a &lt; b {}</pre>\nafter"},
		{"```\nleft open", "<pre>left open</pre>"},
	}
	for _, tt := range tests {
		if got := toHTML(tt.md); got != tt.want {
			t.Errorf("toHTML(%q)\n got %q\nwant %q", tt.md, got, tt.want)
		}
	}
}

func TestSplit(t *testing.T) {
	text := strings.Repeat("a", 60) + "\n" + strings.Repeat("b", 60)
	parts := split(text, 100)
	if len(parts) != 2 || parts[0] != strings.Repeat("a", 60) || parts[1] != strings.Repeat("b", 60) {
		t.Errorf("parts = %q", parts)
	}
	if parts := split(strings.Repeat("é", 250), 100); len(parts) != 3 || len([]rune(parts[2])) != 50 {
		t.Errorf("rune split = %d parts", len(parts))
	}
}

// fakeBot answers sendMessage with the scripted responses and keeps what
// it was sent.
type fakeBot struct {
	mu        sync.Mutex
	responses []string // status code and JSON body, "429 {…}"
	sent      []map[string]any
}

func (f *fakeBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path != "/botTOKEN/sendMessage" {
		http.Error(w, `{"ok":false,"description":"wrong path"}`, 404)
		return
	}
	var p map[string]any
	json.NewDecoder(r.Body).Decode(&p)
	f.sent = append(f.sent, p)
	resp := `200 {"ok":true,"result":{}}`
	if len(f.responses) > 0 {
		resp, f.responses = f.responses[0], f.responses[1:]
	}
	code, body, _ := strings.Cut(resp, " ")
	w.WriteHeader(map[string]int{"200": 200, "400": 400, "429": 429, "502": 502}[code])
	io.WriteString(w, body)
}

func newTest(t *testing.T, responses ...string) (*fakeBot, *Channel) {
	f := &fakeBot{responses: responses}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New("TOKEN", "42")
	c.api = srv.URL
	return f, c
}

func TestSend(t *testing.T) {
	f, c := newTest(t)
	if err := c.Send(context.Background(), tomte.Message{Agent: "chaser", Text: "Sent **2** reminders."}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 {
		t.Fatalf("sent %d", len(f.sent))
	}
	p := f.sent[0]
	if p["chat_id"] != "42" || p["parse_mode"] != "HTML" || p["text"] != "<b>chaser</b>\nSent <b>2</b> reminders." {
		t.Errorf("params = %v", p)
	}
	if lp, _ := p["link_preview_options"].(map[string]any); lp["is_disabled"] != true {
		t.Errorf("link previews on: %v", p)
	}
}

func TestSendFallsBackToPlainText(t *testing.T) {
	f, c := newTest(t, `400 {"ok":false,"description":"Bad Request: can't parse entities"}`)
	if err := c.Send(context.Background(), tomte.Message{Text: "**odd"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 2 || f.sent[1]["parse_mode"] != nil || f.sent[1]["text"] != "**odd" {
		t.Errorf("sent = %v", f.sent)
	}
}

func TestSendRetries(t *testing.T) {
	f, c := newTest(t,
		`429 {"ok":false,"description":"Too Many Requests","parameters":{"retry_after":1}}`,
		`502 {"ok":false,"description":"Bad Gateway"}`,
	)
	if testing.Short() {
		t.Skip("waits 3 s")
	}
	if err := c.Send(context.Background(), tomte.Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 3 {
		t.Errorf("sent %d times", len(f.sent))
	}
}

func TestErrorsHideTheToken(t *testing.T) {
	c := New("SECRET-TOKEN", "42")
	c.api = "http://127.0.0.1:1" // nothing listens
	_, err := c.post(context.Background(), "sendMessage", []byte(`{}`), nil)
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") || !strings.Contains(err.Error(), "refused") {
		t.Errorf("err = %v", err)
	}
	_, c2 := newTest(t, `400 {"ok":false,"description":"Bad Request: chat not found"}`)
	if err := c2.Send(context.Background(), tomte.Message{Text: "hi"}); err == nil || !strings.Contains(err.Error(), "chat not found") {
		t.Errorf("err = %v", err)
	}
}

// fakePoller answers getUpdates with one scripted batch per call, then
// with nothing, and keeps the offsets it was asked for.
type fakePoller struct {
	mu      sync.Mutex
	batches []string
	offsets []float64
}

func (f *fakePoller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var p map[string]any
	json.NewDecoder(r.Body).Decode(&p)
	f.mu.Lock()
	f.offsets = append(f.offsets, p["offset"].(float64))
	batch := `[]`
	if len(f.batches) > 0 {
		batch, f.batches = f.batches[0], f.batches[1:]
	}
	f.mu.Unlock()
	if batch == "fail" {
		w.WriteHeader(409)
		io.WriteString(w, `{"ok":false,"description":"Conflict: terminated by other getUpdates request"}`)
		return
	}
	if batch == `[]` {
		time.Sleep(20 * time.Millisecond) // a long poll with nothing new
	}
	io.WriteString(w, `{"ok":true,"result":`+batch+`}`)
}

func msg(id int, chat int64, text string) string {
	return fmt.Sprintf(`{"update_id":%d,"message":{"text":%q,"chat":{"id":%d}}}`, id, text, chat)
}

func receive(t *testing.T, c *Channel, f *fakePoller, d time.Duration) []tomte.Incoming {
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c.api = srv.URL
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	var got []tomte.Incoming
	if err := c.Receive(ctx, func(in tomte.Incoming) { got = append(got, in) }); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReceiveTakesOnlyItsChat(t *testing.T) {
	f := &fakePoller{batches: []string{
		"[" + msg(7, 42, "/report a.com") + "," + msg(8, 99, "/report b.com") + "]",
		"[" + msg(9, 42, "  ") + `,{"update_id":10}` + "," + msg(11, 42, "hi") + "]",
	}}
	got := receive(t, New("TOKEN", "42"), f, 150*time.Millisecond)
	if len(got) != 2 || got[0] != (tomte.Incoming{From: "42", Text: "/report a.com"}) || got[1].Text != "hi" {
		t.Errorf("received %+v", got)
	}
	// Each poll starts past what was handed over, and the last one, after
	// the stop, says so to Telegram.
	f.mu.Lock()
	defer f.mu.Unlock()
	if o := f.offsets; len(o) < 3 || o[0] != 0 || o[1] != 9 || o[2] != 12 || o[len(o)-1] != 12 {
		t.Errorf("offsets = %v", o)
	}
}

func TestReceivePublic(t *testing.T) {
	f := &fakePoller{batches: []string{"[" + msg(1, 5, "/start") + "," + msg(2, -77, "/help") + "]"}}
	got := receive(t, New("TOKEN", "").Public(), f, 100*time.Millisecond)
	if len(got) != 2 || got[0].From != "5" || got[1].From != "-77" {
		t.Errorf("received %+v", got)
	}
}

func TestReceiveRetries(t *testing.T) {
	f := &fakePoller{batches: []string{"fail", "[" + msg(1, 42, "/x") + "]"}}
	if testing.Short() {
		t.Skip("waits 1 s")
	}
	got := receive(t, New("TOKEN", "42"), f, 1500*time.Millisecond)
	if len(got) != 1 || got[0].Text != "/x" {
		t.Errorf("received %+v", got)
	}
}

func TestReceiveNeedsAChat(t *testing.T) {
	if err := New("TOKEN", "").Receive(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "Public") {
		t.Errorf("err = %v", err)
	}
}

func TestSendTo(t *testing.T) {
	f, c := newTest(t)
	if err := c.Send(context.Background(), tomte.Message{Text: "yours", To: "77"}); err != nil {
		t.Fatal(err)
	}
	if f.sent[0]["chat_id"] != "77" || f.sent[0]["text"] != "yours" {
		t.Errorf("params = %v", f.sent[0])
	}
	// A public bot with no chat of its own sends only To someone.
	c.chat = ""
	if err := c.Send(context.Background(), tomte.Message{Text: "lost"}); err == nil {
		t.Error("sent with no chat")
	}
}
