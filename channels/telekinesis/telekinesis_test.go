package telekinesis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/berkaycubuk/tomte"
)

// fakeServer answers POST /api/messages with the scripted status codes and
// keeps the texts it was sent.
type fakeServer struct {
	mu    sync.Mutex
	codes []int
	auth  []string
	sent  []string
}

func (f *fakeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != "POST" || r.URL.Path != "/api/messages" {
		http.Error(w, "wrong path", 404)
		return
	}
	var p struct{ Text string }
	json.NewDecoder(r.Body).Decode(&p)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.sent = append(f.sent, p.Text)
	code := 200
	if len(f.codes) > 0 {
		code, f.codes = f.codes[0], f.codes[1:]
	}
	if code == 429 {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(code)
	if code == 200 {
		w.Write([]byte(`{"id":1}`))
	} else {
		w.Write([]byte(`{"error":"nope"}`))
	}
}

func newTest(t *testing.T, codes ...int) (*fakeServer, *Channel) {
	f := &fakeServer{codes: codes}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := New("tk_TOKEN")
	c.api = srv.URL
	return f, c
}

func TestSend(t *testing.T) {
	f, c := newTest(t)
	if err := c.Send(context.Background(), tomte.Message{Agent: "hn-digest", Text: "  Sent **2** reminders.\n"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 1 || f.sent[0] != "Sent **2** reminders." || f.auth[0] != "Bearer tk_TOKEN" {
		t.Errorf("sent %q with %q", f.sent, f.auth)
	}
}

func TestSendCutsLongText(t *testing.T) {
	f, c := newTest(t)
	if err := c.Send(context.Background(), tomte.Message{Text: strings.Repeat("é", maxText)}); err != nil {
		t.Fatal(err)
	}
	if s := f.sent[0]; len(s) > maxText || !utf8.ValidString(s) || !strings.HasSuffix(s, "…") {
		t.Errorf("sent %d bytes, valid %v", len(s), utf8.ValidString(s))
	}
}

func TestSendRetries(t *testing.T) {
	if testing.Short() {
		t.Skip("waits 3 s")
	}
	f, c := newTest(t, 429, 502)
	if err := c.Send(context.Background(), tomte.Message{Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 3 {
		t.Errorf("sent %d times", len(f.sent))
	}
}

func TestSendGivesUpOnBadToken(t *testing.T) {
	f, c := newTest(t, 401)
	err := c.Send(context.Background(), tomte.Message{Text: "hi"})
	if err == nil || !strings.Contains(err.Error(), "401") || len(f.sent) != 1 {
		t.Errorf("err = %v after %d tries", err, len(f.sent))
	}
	if err := New("").Send(context.Background(), tomte.Message{Text: "hi"}); err == nil {
		t.Error("no error without a token")
	}
}
