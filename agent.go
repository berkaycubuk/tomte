// Package tomte builds small agents that do one job. An agent is a Go
// program: it declares its instructions, model, tools and channel, and
// compiles to one binary. See docs/design.md.
package tomte

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type Agent struct {
	name         string
	instructions string
	model        *model
	tools        map[string]*tool
	specs        []toolSpec // in declaration order, for the model
	channel      Channel
	maxSteps     int
	errs         []error // mistakes in the options, reported by Do and Run
}

type Option func(*Agent)

// New declares an agent. Mistakes in the options are reported when it
// runs, so the declaration reads as one expression.
func New(name string, opts ...Option) *Agent {
	a := &Agent{
		name:     name,
		tools:    map[string]*tool{},
		channel:  Stdout(),
		maxSteps: 20,
	}
	for _, o := range opts {
		o(a)
	}
	if a.model == nil {
		a.errs = append(a.errs, errors.New("no model: add tomte.Model(baseURL, name, key)"))
	}
	return a
}

// Instructions is the agent's system prompt: who it is and what its job is.
func Instructions(s string) Option {
	return func(a *Agent) { a.instructions = strings.TrimSpace(s) }
}

// Model sets the OpenAI-compatible API the agent thinks with, for example
// Model("https://openrouter.ai/api/v1", "z-ai/glm-4.6", os.Getenv("MODEL_KEY")).
func Model(baseURL, name, key string) Option {
	return func(a *Agent) {
		a.model = &model{baseURL: baseURL, name: name, key: key, http: &http.Client{Timeout: 5 * time.Minute}}
	}
}

// SendTo sets where the agent's results go; it's Stdout if not given.
func SendTo(c Channel) Option {
	return func(a *Agent) { a.channel = c }
}

// MaxSteps caps the model calls in one run; it's 20 if not given.
func MaxSteps(n int) Option {
	return func(a *Agent) { a.maxSteps = n }
}

// ErrStepLimit is returned when a run uses all its steps without replying.
var ErrStepLimit = errors.New("step limit reached")

// Result is what one run did.
type Result struct {
	Text      string   // the model's final reply
	Steps     int      // model calls
	ToolCalls []string // tool names, in the order they were called
	Usage     Usage
}

// Do runs the agent on one task and returns its reply. It doesn't send the
// reply anywhere; Run does that.
func (a *Agent) Do(ctx context.Context, task string) (Result, error) {
	var res Result
	if err := errors.Join(a.errs...); err != nil {
		return res, err
	}
	msgs := []message{
		{Role: "system", Content: a.system(time.Now())},
		{Role: "user", Content: task},
	}
	for res.Steps < a.maxSteps {
		res.Steps++
		reply, usage, err := a.model.chat(ctx, msgs, a.specs)
		res.Usage.add(usage)
		if err != nil {
			return res, err
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 {
			res.Text = strings.TrimSpace(reply.Content)
			return res, nil
		}
		for _, c := range reply.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, c.Function.Name)
			msgs = append(msgs, message{Role: "tool", ToolCallID: c.ID, Content: a.call(ctx, c)})
		}
	}
	return res, ErrStepLimit
}

func (a *Agent) system(now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, an agent that does one job.\n", a.name)
	if a.instructions != "" {
		b.WriteString("\n" + a.instructions + "\n")
	}
	fmt.Fprintf(&b, "\nThe time is %s.", now.Format("Monday, 2 January 2006, 15:04 MST"))
	return b.String()
}

func (a *Agent) call(ctx context.Context, c toolCall) string {
	t, ok := a.tools[c.Function.Name]
	if !ok {
		return fmt.Sprintf("error: there is no tool named %q", c.Function.Name)
	}
	log.Printf("%s: tool %s %s", a.name, c.Function.Name, snippet([]byte(c.Function.Arguments)))
	return t.call(ctx, c.Function.Arguments)
}

// Run is the agent's main: it reads the command line, does what it says
// and exits.
func (a *Agent) Run() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := a.main(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

func (a *Agent) main(ctx context.Context, args []string) int {
	if err := errors.Join(a.errs...); err != nil {
		log.Printf("%s: %v", a.name, err)
		return 2
	}
	if len(args) >= 2 && args[0] == "run" {
		return a.runOnce(ctx, strings.Join(args[1:], " "))
	}
	fmt.Fprintf(os.Stderr, "usage: %s run <task>\n", a.name)
	return 2
}

// runOnce does one task and sends the outcome, failures included, through
// the channel: nobody may be watching the terminal.
func (a *Agent) runOnce(ctx context.Context, task string) int {
	res, err := a.Do(ctx, task)
	text, code := res.Text, 0
	if err != nil {
		text, code = fmt.Sprintf("%s failed after %d steps: %v", a.name, res.Steps, err), 1
	}
	if text == "" {
		return code
	}
	// A run stopped by a signal still says so.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := a.channel.Send(ctx, Message{Agent: a.name, Text: text}); err != nil {
		log.Printf("%s: sending to %s: %v", a.name, a.channel.Name(), err)
		return 1
	}
	return code
}
