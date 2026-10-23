package tomte

// A channel is where an agent's results go. The core only sends; receiving
// messages and asking questions will be optional interfaces a channel can
// add later, found with a type assertion.

import (
	"context"
	"fmt"
	"io"
	"os"
)

// Message is what an agent sends: markdown text, from the named agent.
type Message struct {
	Agent string
	Text  string
}

type Channel interface {
	Name() string
	Send(ctx context.Context, m Message) error
}

// Stdout prints results; it's the default channel.
func Stdout() Channel { return writerChannel{os.Stdout} }

type writerChannel struct{ w io.Writer }

func (writerChannel) Name() string { return "stdout" }

func (c writerChannel) Send(_ context.Context, m Message) error {
	_, err := fmt.Fprintln(c.w, m.Text)
	return err
}
