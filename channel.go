package tomte

// A channel is where an agent's results go. Every channel sends; one that
// people can also write to implements Receiver, which the core finds with
// a type assertion. Asking questions will be another such interface.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Message is what an agent sends: markdown text, from the named agent.
// To is who gets it, in the channel's own terms (a Telegram chat id); a
// channel sends a message without one to its usual recipient.
type Message struct {
	Agent string
	Text  string
	To    string
}

type Channel interface {
	Name() string
	Send(ctx context.Context, m Message) error
}

// Incoming is a message someone wrote to the agent. From is the sender in
// the channel's own terms; a Message To them answers it.
type Incoming struct {
	From string
	Text string
}

// Receiver is a channel people can write to. Receive calls handle with
// each message until ctx is done, and returns then, or earlier when
// receiving can't work at all. handle may wait while the agent is busy.
type Receiver interface {
	Channel
	Receive(ctx context.Context, handle func(Incoming)) error
}

// receivers lists the channels in c that people can write to.
func receivers(c Channel) []Receiver {
	cs, ok := c.(multiChannel)
	if !ok {
		cs = multiChannel{c}
	}
	var rs []Receiver
	for _, c := range cs {
		if r, ok := c.(Receiver); ok {
			rs = append(rs, r)
		}
	}
	return rs
}

// Stdout prints results; it's the default channel.
func Stdout() Channel { return writerChannel{os.Stdout} }

type writerChannel struct{ w io.Writer }

func (writerChannel) Name() string { return "stdout" }

func (c writerChannel) Send(_ context.Context, m Message) error {
	_, err := fmt.Fprintln(c.w, m.Text)
	return err
}

// multiChannel sends to each of its channels, all of them even when one
// fails.
type multiChannel []Channel

func (cs multiChannel) Name() string {
	names := make([]string, len(cs))
	for i, c := range cs {
		names[i] = c.Name()
	}
	return strings.Join(names, ", ")
}

func (cs multiChannel) Send(ctx context.Context, m Message) error {
	var errs []error
	for _, c := range cs {
		if err := c.Send(ctx, m); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name(), err))
		}
	}
	return errors.Join(errs...)
}
