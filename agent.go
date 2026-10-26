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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Agent is one agent: its declaration, from New, and its run log. Run
// is its main; Do runs it on one task from Go code. An Agent is safe to
// use from several goroutines.
type Agent struct {
	name         string
	instructions string
	model        *model
	tools        map[string]*tool
	specs        []toolSpec // in declaration order, for the model
	channel      Channel
	maxSteps     int
	timeout      time.Duration // per run, job or command, 0 for no limit
	price        *price
	maxRun       float64 // dollars per run, 0 for no limit
	maxDay       float64 // dollars per day, 0 for no limit
	logPath      string
	journal      bool // reads the system journal
	schedules    []*schedule
	commands     []*command
	now          func() time.Time
	errs         []error // mistakes in the options, reported by Do and Run

	mu       sync.Mutex
	logged   float64 // spent today by logged runs, read at each run's start
	inflight float64 // spent by runs in this process that aren't logged yet
}

// Option is part of an agent's declaration, given to New: Model, Tool,
// Cron and the rest.
type Option func(*Agent)

// New declares an agent. Mistakes in the options are reported when it
// runs, so the declaration reads as one expression.
func New(name string, opts ...Option) *Agent {
	a := &Agent{
		name:     name,
		tools:    map[string]*tool{},
		channel:  Stdout(),
		maxSteps: 20,
		timeout:  10 * time.Minute,
		logPath:  defaultLogPath(name),
		now:      time.Now,
	}
	for _, o := range opts {
		o(a)
	}
	if a.model == nil && a.needsModel() {
		a.errs = append(a.errs, errors.New("no model: add tomte.Model(baseURL, name, key)"))
	}
	if a.maxSteps < 1 {
		a.errs = append(a.errs, errors.New("MaxSteps needs at least 1"))
	}
	if a.timeout < 0 {
		a.errs = append(a.errs, errors.New("Timeout can't be negative"))
	}
	if a.maxRun < 0 || a.maxDay < 0 {
		a.errs = append(a.errs, errors.New("MaxCost limits can't be negative"))
	}
	if (a.maxRun > 0 || a.maxDay > 0) && a.price == nil {
		a.errs = append(a.errs, errors.New("MaxCost needs the model's price: add tomte.Price(in, out)"))
	}
	if len(a.commands) > 0 && len(receivers(a.channel)) == 0 {
		a.errs = append(a.errs, fmt.Errorf("commands need a channel people can write to, like telegram; %s isn't one", a.channel.Name()))
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
// With several channels each result goes to all of them.
func SendTo(cs ...Channel) Option {
	return func(a *Agent) {
		switch len(cs) {
		case 0:
		case 1:
			a.channel = cs[0]
		default:
			a.channel = multiChannel(cs)
		}
	}
}

// MaxSteps caps the model calls in one run; it's 20 if not given.
func MaxSteps(n int) Option {
	return func(a *Agent) { a.maxSteps = n }
}

// Log sets the file the run log is appended to. It's <name>.jsonl in
// $STATE_DIRECTORY (set by systemd's StateDirectory=) or else in the
// working directory.
func Log(path string) Option {
	return func(a *Agent) { a.logPath = path }
}

func defaultLogPath(name string) string {
	return filepath.Join(StateDir(), name+".jsonl")
}

// StateDir is the directory the agent may keep files in: its own one,
// /var/lib/tomte/<name>, when deployed with the tomte command, and the
// working directory ("") otherwise.
func StateDir() string {
	dir, _, _ := strings.Cut(os.Getenv("STATE_DIRECTORY"), ":")
	return dir
}

// ReadsJournal lets the agent read the system journal, sshd's and sudo's
// lines included, when it's deployed with the tomte command: its user
// joins the systemd-journal group. Nothing else changes.
func ReadsJournal() Option {
	return func(a *Agent) { a.journal = true }
}

// ErrStepLimit is returned when a run uses all its steps without replying.
var ErrStepLimit = errors.New("step limit reached")

// Result is what one run did.
type Result struct {
	Text      string   // the model's final reply
	Steps     int      // model calls
	ToolCalls []string // tool names, in the order they were called
	Usage     Usage
	Cost      float64 // dollars, 0 without a Price
}

// Do runs the agent on one task, logs the run and returns its reply. It
// doesn't send the reply anywhere; Run does that.
func (a *Agent) Do(ctx context.Context, task string) (Result, error) {
	return a.run(ctx, "code", task)
}

func (a *Agent) run(ctx context.Context, trigger, task string) (Result, error) {
	if err := errors.Join(a.errs...); err != nil {
		return Result{}, err
	}
	if a.model == nil {
		return Result{}, fmt.Errorf("no model to do %q with; this agent only does its jobs: %s", cut(task, 60), strings.Join(a.jobs(), ", "))
	}
	start := a.now()
	var res Result
	b, err := a.startBudget(start)
	if err == nil {
		lctx, cancel := a.limit(ctx)
		res, err = a.loop(lctx, trigger, task, start, b)
		if err != nil && lctx.Err() != nil {
			err = context.Cause(lctx) // the model's call says only "context deadline exceeded"
		}
		cancel()
	}
	// The log takes over counting the run's spending, in one step, so a
	// run starting meanwhile counts it once.
	a.mu.Lock()
	a.logRun(start, trigger, task, res, err)
	if b != nil {
		a.inflight -= b.spent
		a.logged += b.spent
	}
	a.mu.Unlock()
	return res, err
}

func (a *Agent) loop(ctx context.Context, trigger, task string, start time.Time, b *budget) (res Result, err error) {
	msgs := []message{
		{Role: "system", Content: a.system(trigger, start)},
		{Role: "user", Content: task},
	}
	for res.Steps < a.maxSteps {
		res.Steps++
		reply, usage, err := a.model.chat(ctx, msgs, a.specs)
		over := b.spend(&res, usage)
		if err != nil {
			return res, err
		}
		msgs = append(msgs, reply)
		if len(reply.ToolCalls) == 0 {
			res.Text = strings.TrimSpace(reply.Content)
			return res, nil
		}
		// Over a limit, the run stops before doing more; a reply that
		// came with the call that crossed it is still kept above.
		if over != nil {
			return res, over
		}
		for _, c := range reply.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, c.Function.Name)
			out, err := within(ctx, func() (string, error) { return a.call(ctx, c), nil })
			if err != nil {
				return res, err
			}
			msgs = append(msgs, message{Role: "tool", ToolCallID: c.ID, Content: out})
		}
	}
	return res, ErrStepLimit
}

// nothing is the reply of a scheduled run with nothing worth sending.
const nothing = "NOTHING"

func (a *Agent) system(trigger string, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are %s, an agent that does one job.\n", a.name)
	if a.instructions != "" {
		b.WriteString("\n" + a.instructions + "\n")
	}
	fmt.Fprintf(&b, "\nThe time is %s.", now.Format("Monday, 2 January 2006, 15:04 MST"))
	if trigger == "cron" {
		b.WriteString(" This run was started by a schedule, and nobody is waiting for it. " +
			"Your reply is sent to the people you work for; if there's nothing worth telling them, reply with exactly " + nothing + ".")
	}
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
	if len(args) == 1 && args[0] == "info" {
		return a.printInfo(os.Stdout)
	}
	if err := errors.Join(a.errs...); err != nil {
		log.Printf("%s: %v", a.name, err)
		return 2
	}
	switch {
	case len(args) >= 2 && args[0] == "run":
		return a.runOnce(ctx, "cli", strings.Join(args[1:], " "))
	case len(args) == 0 || len(args) == 1 && args[0] == "serve":
		if err := a.serve(ctx); err != nil {
			log.Printf("%s: %v", a.name, err)
			return 2
		}
		return 0
	}
	fmt.Fprintf(os.Stderr, "usage: %[1]s [serve]     run on the schedules\n       %[1]s run <task>  do one task, or job, now\n       %[1]s info        describe the agent as JSON\n", a.name)
	return 2
}

// runOnce does one task and sends the outcome, failures included, through
// the channel: nobody may be watching the terminal.
func (a *Agent) runOnce(ctx context.Context, trigger, task string) int {
	var text string
	var err error
	if s := a.job(task); s != nil {
		if text, err = a.runJob(ctx, trigger, s); err != nil {
			text = fmt.Sprintf("%s's job %s failed: %v", a.name, s.task, err)
		}
	} else {
		var res Result
		if res, err = a.run(ctx, trigger, task); err != nil {
			text = a.failure(res, err)
		} else {
			text = res.Text
		}
	}
	code := 0
	if err != nil {
		code = 1
	}
	if text == "" || text == nothing {
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

func (a *Agent) failure(res Result, err error) string {
	switch {
	case res.Steps == 0:
		return fmt.Sprintf("%s didn't run: %v", a.name, err)
	case errors.Is(err, ErrCostLimit), errors.Is(err, ErrDailyLimit), errors.Is(err, ErrStepLimit),
		errors.Is(err, ErrTimeLimit):
		return fmt.Sprintf("%s stopped after %s: %v", a.name, steps(res.Steps), err)
	}
	return fmt.Sprintf("%s failed after %s: %v", a.name, steps(res.Steps), err)
}

func steps(n int) string {
	if n == 1 {
		return "1 step"
	}
	return fmt.Sprintf("%d steps", n)
}
