package tomte

// Commands: people write to the agent through a channel that receives,
// and Go code answers, with no model, so a stranger writing to a public
// bot can't spend anything. "/report example.com" runs the report
// command with "example.com" as its argument, and what it returns goes
// back to whoever wrote. Anything that isn't a declared command, /help
// and /start included unless declared, gets the list of commands.

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Request is a command someone sent.
type Request struct {
	From string // the sender, in the channel's terms: a Telegram chat id
	Args string // what followed the command's name, trimmed
}

type command struct {
	name, description string
	hidden            bool // left out of the list of commands
	fn                func(context.Context, Request) (string, error)
}

// Telegram's rule for command names, which suits any channel.
var commandName = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// Command answers "/name …" sent to the agent through its channel, while
// it's serving. What fn returns goes back to the sender unless it's empty
// or NOTHING. An error is logged, and the sender only hears it didn't
// work: errors are written for whoever runs the agent, not for strangers.
func Command(name, description string, fn func(context.Context, Request) (string, error)) Option {
	return declare(command{name: name, description: description, fn: fn})
}

// HiddenCommand is a Command left out of the list of commands, for one
// only the agent's owner uses, like /approve on a public bot. Anyone can
// still send it, so fn checks who's asking.
func HiddenCommand(name string, fn func(context.Context, Request) (string, error)) Option {
	return declare(command{name: name, hidden: true, fn: fn})
}

func declare(c command) Option {
	return func(a *Agent) {
		name := strings.TrimPrefix(c.name, "/")
		if !commandName.MatchString(name) {
			a.errs = append(a.errs, fmt.Errorf("command %q: use 1 to 32 lowercase letters, digits and _", name))
			return
		}
		if a.command(name) != nil {
			a.errs = append(a.errs, fmt.Errorf("command %s is declared twice", name))
			return
		}
		c.name, c.description = name, strings.TrimSpace(c.description)
		a.commands = append(a.commands, &c)
	}
}

// command is the command named name, or nil.
func (a *Agent) command(name string) *command {
	for _, c := range a.commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// parseCommand reads "/name args", and "/name@bot args" as Telegram sends
// it in groups.
func parseCommand(text string) (name, args string, ok bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	word, args := text[1:], ""
	if i := strings.IndexFunc(word, unicode.IsSpace); i >= 0 {
		word, args = word[:i], strings.TrimSpace(word[i:])
	}
	word, _, _ = strings.Cut(word, "@")
	return strings.ToLower(word), args, true
}

func (a *Agent) help() string {
	var b strings.Builder
	b.WriteString("Commands:")
	for _, c := range a.commands {
		if c.hidden {
			continue
		}
		fmt.Fprintf(&b, "\n/%s", c.name)
		if c.description != "" {
			b.WriteString(": " + c.description)
		}
	}
	if a.command("help") == nil {
		b.WriteString("\n/help: this list")
	}
	return b.String()
}

// answer does what in asks and sends the reply back to its sender
// through r.
func (a *Agent) answer(ctx context.Context, r Receiver, in Incoming) {
	text := a.help()
	if name, args, ok := parseCommand(in.Text); ok {
		if c := a.command(name); c != nil {
			var err error
			if text, err = a.runCommand(ctx, r.Name(), c, Request{From: in.From, Args: args}, in.Text); err != nil {
				log.Printf("%s: /%s from %s: %v", a.name, c.name, in.From, err)
				text = fmt.Sprintf("Sorry, /%s didn't work. Try again later.", c.name)
			}
		} else if name != "help" && name != "start" {
			text = fmt.Sprintf("There's no /%s.\n\n%s", name, text)
		}
	}
	if text == "" || text == nothing {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := r.Send(ctx, Message{Text: text, To: in.From}); err != nil {
		log.Printf("%s: answering %s on %s: %v", a.name, in.From, r.Name(), err)
	}
}

// runCommand runs c and logs it, with the channel as its trigger.
func (a *Agent) runCommand(ctx context.Context, trigger string, c *command, req Request, task string) (text string, err error) {
	start := a.now()
	ctx, cancel := a.limit(ctx)
	defer cancel()
	text, err = within(ctx, func() (text string, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panicked: %v", r)
			}
		}()
		return c.fn(ctx, req)
	})
	text = strings.TrimSpace(text)
	a.mu.Lock()
	a.logRun(start, trigger, task, Result{Text: text}, err)
	a.mu.Unlock()
	return text, err
}

// maxAnswering is how many commands an agent answers at once; more wait.
const maxAnswering = 8

// receive starts taking messages from every channel that receives. It
// stops when ctx is done; answers in progress run on with runCtx, and wg
// counts both.
func (a *Agent) receive(ctx, runCtx context.Context, wg *sync.WaitGroup) {
	busy := make(chan struct{}, maxAnswering)
	for _, r := range receivers(a.channel) {
		log.Printf("%s: answering %s on %s", a.name, a.commandList(), r.Name())
		wg.Go(func() {
			err := r.Receive(ctx, func(in Incoming) {
				busy <- struct{}{}
				wg.Go(func() {
					defer func() { <-busy }()
					a.answer(runCtx, r, in)
				})
			})
			if err != nil {
				log.Printf("%s: receiving on %s: %v", a.name, r.Name(), err)
			}
		})
	}
}

func (a *Agent) commandList() string {
	names := make([]string, len(a.commands))
	for i, c := range a.commands {
		names[i] = "/" + c.name
	}
	return strings.Join(names, ", ")
}
