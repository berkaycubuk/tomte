# Tomte's design

Tomte is a Go toolkit for small agents that do one job. A tomte is the
farm spirit that quietly does the chores at night; a Tomte agent is the
same: one task, done on a schedule or when asked, without supervision.

This document records what the first version of Tomte is. It was written
on 2026-10-23, before any code, and keeps only what an agent can't work
without.

## In one paragraph

An agent is a Go program that imports `tomte`, declares its instructions,
model, tools, schedule and channel, and compiles to one small static
binary. The binary runs the agent loop (model → tool calls → model) when
the schedule fires or when it's run from the command line, stops at a
step and dollar limit, sends the result through its channel, and appends
one line about the run to a log file. Many agents should fit on a small
computer.

## Goals

- **Small.** The core uses only Go's standard library and stays around
  1,000–1,500 lines. A binary is under 10 MB.
- **Light when idle.** No polling while waiting for the schedule: an
  idle agent sleeps. Aim for 10–15 MB of memory per idle agent, so fifty
  fit in well under 1 GB.
- **Safe unattended.** Nobody watches a run, so limits are not optional.

## What it isn't

Tomte isn't a personal assistant; it's for many small agents, each
doing one job. So Tomte has no browser, no sandboxes, no
long-term memory, no user accounts and no code written by the model.
Tools are Go code, and configuration is Go code: no YAML, no plugins.

## An agent

```go
package main

import (
	"context"
	_ "embed"
	"os"

	"github.com/berkaycubuk/tomte"
	"github.com/berkaycubuk/tomte/channels/telegram"
)

//go:embed instructions.md
var instructions string

func main() {
	tomte.New("invoice-chaser",
		tomte.Instructions(instructions),
		tomte.Model("https://openrouter.ai/api/v1", "z-ai/glm-4.6", os.Getenv("MODEL_KEY")),
		tomte.Tool("find_overdue", "List unpaid invoices older than N days", findOverdue),
		tomte.Tool("send_reminder", "Email a payment reminder", sendReminder),
		tomte.Cron("0 9 * * 1-5", "Chase invoices overdue by 14 days or more"),
		tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), "12345")),
		tomte.Price(0.60, 2.20),   // dollars per million tokens in, out
		tomte.MaxCost(0.50, 5.00), // dollars per run, per day
		tomte.MaxSteps(20),
	).Run()
}

type overdueArgs struct {
	Days int `json:"days" jsonschema:"minimum age in days"`
}

func findOverdue(ctx context.Context, in overdueArgs) ([]Invoice, error) { … }
```

## The core

| Part | What it does |
|---|---|
| Loop | Builds the prompt, calls the model, runs the tool calls, repeats until the model replies |
| Model | One OpenAI-compatible chat client (OpenRouter, z.ai, Groq, Ollama…); counts tokens from each response and prices them |
| Tools | `func(context.Context, In) (Out, error)`; the JSON schema comes from `In`, `Out` is marshalled to JSON for the model; a tool's error goes back to the model as its result |
| Triggers | `Cron` (five fields, a shortcut like `@daily`, or `@every 30m`) while serving: `./invoice-chaser` or `./invoice-chaser serve` sleeps until a schedule is due; and the command line: `./invoice-chaser run "…"`. A scheduled run that has nothing to say replies `NOTHING` and sends nothing |
| Channel | Where the result goes, behind an interface; v1 ships one channel |
| Limits | Steps per run, dollars per run and per day; at a limit the run stops and says so through the channel |
| Run log | One JSON line per run in a file: when, trigger, tools called, steps, tokens, cost, result or error |
| Secrets | Environment variables, read by the agent's own code |

### Channels

Every way of reaching people sits behind one interface, so Slack or email
later are new packages under `channels/`, not changes to the core.

```go
type Channel interface {
	Name() string
	Send(ctx context.Context, m Message) error
}
```

`Message` is markdown text. Receiving messages and asking questions are
left out of v1; they will be optional interfaces (`Receiver`, `Asker`)
that a channel implements and the core finds with a type assertion, so
adding them doesn't break existing channels.

An agent picks its channel with `tomte.SendTo(…)`; without one it uses
`tomte.Stdout()`. v1 ships `stdout` and `telegram`.

The Telegram channel turns the model's markdown into the HTML Telegram
takes (and sends plain text when Telegram can't parse it), starts each
message with the agent's name so several agents can share a bot and a
chat, and turns link previews off.

### Deploying

An agent is one binary and an env file in `/srv/tomte/<name>/`, started
with `tomte@.service`, one unit for every agent on the machine. Each runs
as its own throwaway user with a private state directory for its run
log, capped at 128 MB and half a CPU, inside `tomte.slice`, which caps all
agents together so they share a machine with other programs politely.

The `tomte` command (`cmd/tomte`) does the deploying from your own
machine, over ssh. It's a client, not a manager: systemd already keeps
agents running, restarts them, limits them and keeps their logs, so
nothing else runs on the box. The box is the source of truth: `tomte ls`
reads what's in `/srv/tomte`, and there's no inventory to keep in step.

- `tomte setup box` installs the unit, the slice and `tomte-helper`, a
  short script that does everything needing root, with a sudoers rule
  letting the ssh user run only that script. It accepts nothing but agent
  names, so deploying doesn't need root on a box that runs other things.
- `tomte deploy box ./pkg` builds for the box's architecture, uploads
  the binary under a random id, and asks it its name with `./agent info`
  (one line of JSON: name, schedules and their next runs, limits). Only
  then does it replace `/srv/tomte/<name>/<name>`, keeping the old one as
  `<name>.prev`, and restart the unit. The name comes from the code, so
  the unit, the directory and the run log can't disagree.
- `tomte ls box` shows every agent's state, memory, next and last run,
  and what it spent today.

## Later

Added when an agent needs them, in roughly this order: receiving messages
over a channel, approvals before risky tools, webhooks, model fallbacks,
resuming a run after a restart, more `tomte` commands (`logs`, `runs`,
`run`, `env`, `rollback`, `rm`), `tomte new`, MCP tools, test helpers,
and several agents in one process to share memory on very small machines.

## First version

1. Model client, loop, typed tools, `run` from the command line, stdout
2. Limits and the run log
3. Cron
4. Telegram channel
5. An example agent in `examples/`
