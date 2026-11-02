// Package telekinesis sends an agent's results to a person through
// telekinesis (https://telekinesis.berkay.tools), which shows them in its
// web app with a push notification. Telekinesis is made by Tomte's
// author. Make the agent a token in Settings → Agents:
//
//	tomte.SendTo(telekinesis.New(os.Getenv("TK_TOKEN")))
//
// The token names the agent, so messages go as they are, without the
// agent's name in front. The channel also receives: what the person
// writes in the agent's conversation reaches its commands.
package telekinesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/berkaycubuk/tomte"
)

// maxText is the API's limit on a message's text, in bytes.
const maxText = 32 << 10

// Channel sends to one agent's conversation in telekinesis.
type Channel struct {
	token string
	api   string // the server's base URL, changed by tests
	http  *http.Client
}

// New is a channel with an agent token from telekinesis, which names the
// agent and the person it writes to.
func New(token string) *Channel {
	return &Channel{token: token, api: "https://telekinesis.berkay.tools", http: &http.Client{Timeout: 30 * time.Second}}
}

func (*Channel) Name() string { return "telekinesis" }

// Send posts the message to the agent's conversation, cut to the API's
// 32 KB limit: each message is a notification, so it isn't split.
func (c *Channel) Send(ctx context.Context, m tomte.Message) error {
	if c.token == "" {
		return errors.New("telekinesis: no token")
	}
	text := strings.TrimSpace(m.TextOnly())
	if len(text) > maxText {
		text = text[:maxText-len("…")]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
		text += "…"
	}
	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		wait, err := c.post(ctx, body)
		if err == nil {
			return nil
		}
		lastErr = err
		if wait < 0 {
			break
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}

// pollSeconds is how long one read waits for a message, under the
// client's timeout.
const pollSeconds = 25

// Receive long-polls the agent's conversation and hands each message the
// person writes in it to handle, From "human". It starts from now: what
// was written while the agent wasn't running isn't answered. A bad token
// stops it; the server being unreachable is logged and retried until ctx
// is done.
func (c *Channel) Receive(ctx context.Context, handle func(tomte.Incoming)) error {
	if c.token == "" {
		return errors.New("telekinesis: no token")
	}
	after, started, wait := 0, false, time.Second
	for {
		var err error
		if !started {
			var r struct{ Last int }
			if err = c.get(ctx, "/api/channels", &r); err == nil {
				after, started = r.Last, true
			}
		} else {
			var r struct {
				Messages []struct {
					ID   int    `json:"id"`
					From string `json:"from"`
					Text string `json:"text"`
				}
			}
			if err = c.get(ctx, fmt.Sprintf("/api/messages?after=%d&wait=%d", after, pollSeconds), &r); err == nil {
				for _, m := range r.Messages {
					after = max(after, m.ID)
					if m.From == "human" && strings.TrimSpace(m.Text) != "" {
						handle(tomte.Incoming{From: "human", Text: m.Text})
					}
				}
			}
		}
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errBadToken) {
			return err
		}
		if err != nil {
			log.Printf("%v; trying again in %s", err, wait)
			select {
			case <-time.After(wait):
			case <-ctx.Done():
			}
			wait = min(2*wait, time.Minute)
			continue
		}
		wait = time.Second
	}
}

var errBadToken = errors.New("telekinesis: the token was refused")

func (c *Channel) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.api+path, nil)
	if err != nil {
		return fmt.Errorf("telekinesis: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("telekinesis: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("%w: %s", errBadToken, resp.Status)
	}
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("telekinesis: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// post returns how long to wait before trying again, or -1 when trying
// again won't help.
func (c *Channel) post(ctx context.Context, body []byte) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.api+"/api/messages", bytes.NewReader(body))
	if err != nil {
		return -1, fmt.Errorf("telekinesis: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return 2 * time.Second, fmt.Errorf("telekinesis: %v", err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 == 2 {
		return 0, nil
	}
	err = fmt.Errorf("telekinesis: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	switch {
	case resp.StatusCode == 429:
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return time.Duration(max(secs, 1)) * time.Second, err
	case resp.StatusCode >= 500:
		return 2 * time.Second, err
	}
	return -1, err
}
