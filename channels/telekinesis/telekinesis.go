// Package telekinesis sends an agent's results to a person through
// telekinesis (https://telekinesis.berkay.tools), which shows them in its
// web app with a push notification. Make the agent a token in Settings →
// Agents:
//
//	tomte.SendTo(telekinesis.New(os.Getenv("TK_TOKEN")))
//
// The token names the agent, so messages go as they are, without the
// agent's name in front.
package telekinesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/berkaycubuk/tomte"
)

// maxText is the API's limit on a message's text, in bytes.
const maxText = 32 << 10

type Channel struct {
	token string
	api   string // the server's base URL, changed by tests
	http  *http.Client
}

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
	text := strings.TrimSpace(m.Text)
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
