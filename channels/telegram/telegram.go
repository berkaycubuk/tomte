// Package telegram sends an agent's results to a Telegram chat through a
// bot. Create the bot with @BotFather, send it a message, and use your chat
// id (or "@channelname" for a channel the bot is an admin of):
//
//	tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT")))
//
// Several agents can share one bot and one chat; each message starts with
// the agent's name.
//
// People can also write to an agent with tomte.Command: the bot then
// takes commands from that chat. Only one program can read a bot's
// messages, so an agent with commands needs a bot of its own, with no
// webhook set. A Public bot takes commands from anyone who finds it:
//
//	tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT")).Public())
package telegram

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

	"github.com/berkaycubuk/tomte"
)

// Channel sends to a Telegram chat through a bot, and takes commands
// from it.
type Channel struct {
	token, chat string
	public      bool
	api         string // the Bot API's base URL, changed by tests
	http        *http.Client
}

// New is a channel to chat, a chat id or "@channelname", through the bot
// with token, from @BotFather. A message with To set goes to that chat
// instead; with To always set, chat may be "" on a Public bot.
func New(token, chat string) *Channel {
	return &Channel{token: token, chat: chat, api: "https://api.telegram.org", http: &http.Client{Timeout: 30 * time.Second}}
}

// Public lets anyone who finds the bot write to the agent; otherwise only
// the chat given to New can. A command on a public bot learns who wrote
// from tomte.Request's From and decides for itself what they may do.
func (c *Channel) Public() *Channel {
	c.public = true
	return c
}

func (*Channel) Name() string { return "telegram" }

// Send sends the message as HTML made from its markdown, split under
// Telegram's 4096 character limit, to the chat it's To or else the
// channel's own. A part Telegram can't parse is sent again as plain text.
func (c *Channel) Send(ctx context.Context, m tomte.Message) error {
	chat := c.chat
	if m.To != "" {
		chat = m.To
	}
	if c.token == "" || chat == "" {
		return errors.New("telegram: no bot token or chat id")
	}
	text := strings.TrimSpace(m.Text)
	if m.Agent != "" {
		text = "**" + m.Agent + "**\n" + text
	}
	for _, part := range split(text, 3500) {
		err := c.send(ctx, chat, map[string]any{"text": toHTML(part), "parse_mode": "HTML"})
		if err != nil && strings.Contains(err.Error(), "can't parse entities") {
			err = c.send(ctx, chat, map[string]any{"text": part})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// send calls sendMessage, retrying when Telegram is busy or unreachable.
func (c *Channel) send(ctx context.Context, chat string, params map[string]any) error {
	params["chat_id"] = chat
	// Telegram fetches a preview of the first link as soon as a message is
	// sent; an agent's links shouldn't be fetched unless someone taps them.
	params["link_preview_options"] = map[string]bool{"is_disabled": true}
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		wait, err := c.post(ctx, "sendMessage", body, nil)
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

// post calls a Bot API method and decodes what it returns into result,
// unless that's nil. It returns how long to wait before trying again, or
// -1 when trying again won't help. Errors never contain the URL, which
// holds the token.
func (c *Channel) post(ctx context.Context, method string, body []byte, result any) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.api+"/bot"+c.token+"/"+method, bytes.NewReader(body))
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
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return 2 * time.Second, fmt.Errorf("telegram: %s: decode: %v", resp.Status, err)
	}
	if r.OK {
		if result != nil {
			if err := json.Unmarshal(r.Result, result); err != nil {
				return -1, fmt.Errorf("telegram: %s: decode result: %v", method, err)
			}
		}
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

// pollSeconds is how long one getUpdates waits for a message, under the
// client's 30 s timeout.
const pollSeconds = 25

type update struct {
	ID      int `json:"update_id"`
	Message *struct {
		Text string `json:"text"`
		Chat struct {
			ID int64 `json:"id"`
		} `json:"chat"`
	} `json:"message"`
}

// Receive long-polls the bot for messages and hands each text from the
// channel's chat, or from anyone on a Public bot, to handle. Others are
// dropped unread. Telegram being unreachable, or another program reading
// the bot's messages, is logged and retried until ctx is done.
func (c *Channel) Receive(ctx context.Context, handle func(tomte.Incoming)) error {
	if c.token == "" {
		return errors.New("telegram: no bot token")
	}
	if c.chat == "" && !c.public {
		return errors.New("telegram: no chat id to take commands from; give one, or make the bot Public")
	}
	offset, wait := 0, time.Second
	for {
		var updates []update
		_, err := c.getUpdates(ctx, offset, pollSeconds, &updates)
		if ctx.Err() != nil {
			// Telegram forgets messages once a later poll starts past
			// them; one more says so for those already handled, so a
			// restart doesn't answer them again.
			if offset > 0 {
				ack, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				c.getUpdates(ack, offset, 0, nil)
				cancel()
			}
			return nil
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
		for _, u := range updates {
			offset = u.ID + 1
			if u.Message == nil || strings.TrimSpace(u.Message.Text) == "" {
				continue
			}
			from := strconv.FormatInt(u.Message.Chat.ID, 10)
			if !c.public && from != c.chat {
				continue
			}
			handle(tomte.Incoming{From: from, Text: u.Message.Text})
		}
	}
}

func (c *Channel) getUpdates(ctx context.Context, offset, timeout int, updates *[]update) (time.Duration, error) {
	body, err := json.Marshal(map[string]any{"offset": offset, "timeout": timeout, "allowed_updates": []string{"message"}})
	if err != nil {
		return -1, err
	}
	var result any
	if updates != nil {
		result = updates
	}
	return c.post(ctx, "getUpdates", body, result)
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
