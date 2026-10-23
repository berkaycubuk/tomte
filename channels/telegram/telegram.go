// Package telegram sends an agent's results to a Telegram chat through a
// bot. Create the bot with @BotFather, send it a message, and use your chat
// id (or "@channelname" for a channel the bot is an admin of):
//
//	tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT")))
//
// Several agents can share one bot and one chat; each message starts with
// the agent's name.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/berkaycubuk/tomte"
)

type Channel struct {
	token, chat string
	api         string // the Bot API's base URL, changed by tests
	http        *http.Client
}

func New(token, chat string) *Channel {
	return &Channel{token: token, chat: chat, api: "https://api.telegram.org", http: &http.Client{Timeout: 30 * time.Second}}
}

func (*Channel) Name() string { return "telegram" }

// Send sends the message as HTML made from its markdown, split under
// Telegram's 4096 character limit. A part Telegram can't parse is sent
// again as plain text.
func (c *Channel) Send(ctx context.Context, m tomte.Message) error {
	if c.token == "" || c.chat == "" {
		return errors.New("telegram: no bot token or chat id")
	}
	text := strings.TrimSpace(m.Text)
	if m.Agent != "" {
		text = "**" + m.Agent + "**\n" + text
	}
	for _, part := range split(text, 3500) {
		err := c.send(ctx, map[string]any{"text": toHTML(part), "parse_mode": "HTML"})
		if err != nil && strings.Contains(err.Error(), "can't parse entities") {
			err = c.send(ctx, map[string]any{"text": part})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// send calls sendMessage, retrying when Telegram is busy or unreachable.
func (c *Channel) send(ctx context.Context, params map[string]any) error {
	params["chat_id"] = c.chat
	// Telegram fetches a preview of the first link as soon as a message is
	// sent; an agent's links shouldn't be fetched unless someone taps them.
	params["link_preview_options"] = map[string]bool{"is_disabled": true}
	body, err := json.Marshal(params)
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

// post returns how long to wait before trying again, or -1 when trying
// again won't help. Errors never contain the URL, which holds the token.
func (c *Channel) post(ctx context.Context, body []byte) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.api+"/bot"+c.token+"/sendMessage", bytes.NewReader(body))
	if err != nil {
		return -1, errors.New("telegram: bad request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		var uerr interface{ Unwrap() error }
		if errors.As(err, &uerr) && uerr.Unwrap() != nil {
			err = uerr.Unwrap()
		}
		return 2 * time.Second, fmt.Errorf("telegram: %s", strings.ReplaceAll(err.Error(), c.token, "<token>"))
	}
	defer resp.Body.Close()
	var r struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return 2 * time.Second, fmt.Errorf("telegram: %s: decode: %v", resp.Status, err)
	}
	if r.OK {
		return 0, nil
	}
	err = fmt.Errorf("telegram: %s: %s", resp.Status, r.Description)
	switch {
	case resp.StatusCode == 429:
		return time.Duration(max(r.Parameters.RetryAfter, 1)) * time.Second, err
	case resp.StatusCode >= 500:
		return 2 * time.Second, err
	}
	return -1, err
}

// split cuts text into parts of at most max runes, at a line break when
// there's one in the second half.
func split(s string, max int) []string {
	var out []string
	r := []rune(s)
	for len(r) > max {
		cut := max
		for i := max; i > max/2; i-- {
			if r[i] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = []rune(strings.TrimLeft(string(r[cut:]), "\n"))
	}
	if strings.TrimSpace(string(r)) != "" {
		out = append(out, string(r))
	}
	return out
}
