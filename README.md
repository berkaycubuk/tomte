# Tomte

[![test](https://github.com/berkaycubuk/tomte/actions/workflows/test.yml/badge.svg)](https://github.com/berkaycubuk/tomte/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/berkaycubuk/tomte.svg)](https://pkg.go.dev/github.com/berkaycubuk/tomte)

Small agents that do one job, in Go.

A tomte is the farm spirit that quietly does the chores at night. A Tomte
agent is a Go program that does one task, on a schedule or when asked, and
compiles to one small binary: about 6 MB, and about 11 MB of memory while
it waits. Dozens fit on a small machine.

- **Safe to leave alone.** Every run has a step, time and dollar limit,
  and a run that fails or stops says so through the agent's channel.
- **Just Go.** Tools, jobs and commands are Go functions, and the
  configuration is Go code: no YAML, no plugins, no dependencies.
- **One command to deploy.** `tomte deploy box ./agent` builds it,
  uploads it over ssh and runs it as a sandboxed systemd service. Nothing
  else runs on the machine.

**Status:** early. Its author runs agents with it, but the API may
change before v1.0.

```go
package main

import (
	"context"
	"os"

	"github.com/berkaycubuk/tomte"
	"github.com/berkaycubuk/tomte/channels/telegram"
)

type args struct {
	Days int `json:"days" jsonschema:"minimum age in days"`
}

func findOverdue(ctx context.Context, in args) ([]Invoice, error) { … }

func main() {
	tomte.New("invoice-chaser",
		tomte.Instructions("You chase unpaid invoices. …"),
		tomte.Model("https://openrouter.ai/api/v1", "z-ai/glm-4.6", os.Getenv("MODEL_KEY")),
		tomte.Tool("find_overdue", "List unpaid invoices older than N days", findOverdue),
		tomte.Cron("0 9 * * 1-5", "Chase invoices overdue by 14 days or more."),
		tomte.SendTo(telegram.New(os.Getenv("TELEGRAM_TOKEN"), os.Getenv("TELEGRAM_CHAT"))),
		tomte.Price(0.60, 2.20),
		tomte.MaxCost(0.50, 5.00),
	).Run()
}
```

```
./invoice-chaser run "chase the invoices from March"   # one task, now
./invoice-chaser                                       # serve: run on the schedules
./invoice-chaser info                                  # what it is, as JSON
```

Tomte uses only Go's standard library. See [docs/design.md](docs/design.md)
for what it is and isn't, and [examples/](examples/) for agents to start
from.

## An agent

**Model.** `tomte.Model(baseURL, model, key)` takes any OpenAI-compatible
chat completions API: OpenRouter, z.ai, Groq, OpenAI, a local Ollama
(`http://localhost:11434/v1`). Failed calls (network, 429, 5xx) are retried.

**Instructions.** `tomte.Instructions(text)` is the system prompt: what
the job is and how to do it. Keeping it in `instructions.md` and embedding
it with `//go:embed` reads well.

**Tools.** `tomte.Tool(name, description, fn)` with
`fn func(context.Context, In) (Out, error)`. `In` is a struct; its fields
are the tool's arguments, named by their `json` tag, required unless
`omitempty`, described by their `jsonschema` tag. `Out` goes back to the
model as JSON, or as-is when it's a string. Errors and panics go back to
the model too, so it can try something else. Results over 32 KB are cut.

**Schedules.** `tomte.Cron(spec, task)`, as many as you like. A spec is
five cron fields in the machine's time zone (`*/15 9-17 * * mon-fri`), a
shortcut (`@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`) or
`@every 30m`. A scheduled run that has nothing worth saying replies
`NOTHING`, and nothing is sent. If a schedule's last run is still going,
its next turn is skipped.

**Jobs.** `tomte.Job(spec, name, fn)` with
`fn func(context.Context) (string, error)` runs Go code on a schedule,
with no model, for chores that need no judgment. What it returns is sent
through the channel unless it's empty or `NOTHING`, an error is sent as a
failure, and its runs are in the run log like the model's. `./agent run
<name>` runs one now. An agent with only jobs needs no `tomte.Model`.
[examples/ssh-watch](examples/ssh-watch) is one.

**Channel.** `tomte.SendTo(channel)` sets where results and failures go;
it's stdout if not given, and with several channels each result goes to
all of them. `channels/telegram` sends to a Telegram chat and
`channels/telekinesis` to [telekinesis](https://telekinesis.berkay.tools),
a web app with push notifications by Tomte's author. A channel is two
methods, `Name()` and `Send(ctx, Message)`, so writing your own is short. A `Message` with `To` set goes to that recipient (a Telegram
chat id) instead of the channel's own, so a job can write to many people.
A `Message` can carry `Files`, like a report as a PDF: `channels/telegram`
sends each as a document after the text, and a channel that takes text
only, like stdout, names the files it left out.

**Commands.** `tomte.Command(name, description, fn)` with
`fn func(context.Context, tomte.Request) (string, error)` lets people write
to the agent while it's serving: `/report example.com` runs the `report`
command with `Request.Args` set to `example.com` and `Request.From` to the
sender, and what `fn` returns goes back to them. Commands are Go code, not
the model, so writing to the agent can't spend anything. Anything else,
`/help` and `/start` included unless you declare them, gets the list of
commands. `tomte.HiddenCommand(name, description, fn)` is one left out
of that list, for the owner's commands on a public bot; anyone can
still send it, so `fn` checks who's asking. `tomte.Owner(from)` names
the sender who sees the hidden ones listed after the rest. An error is logged and the sender only hears
it didn't work.
Commands are in the run log, with the channel as their trigger, and an
agent with only commands needs no model and no schedule.

Commands need a channel people can write to; `channels/telegram` is one.
It takes commands from its own chat only, unless it's made public:

```go
bot := telegram.New(os.Getenv("TELEGRAM_TOKEN"), "").Public() // anyone who finds the bot
tomte.New("site-check",
	tomte.Command("report", "Check a site now", report),
	tomte.SendTo(bot),
).Run()
```

Only one program can read a bot's messages, so an agent with commands needs
a bot of its own, with no webhook set. A public bot's commands decide for
themselves, from `Request.From`, what a stranger may do.

**Limits.** Nobody watches a scheduled run, so:

- `tomte.MaxSteps(n)`: model calls per run, 20 if not given.
- `tomte.Timeout(d)`: how long a run, job or command may take, 10 minutes
  if not given; 0 means none. A run that reaches it stops waiting, even
  for a tool that ignores its context.
- `tomte.Price(in, out)`: the model's price in dollars per million tokens.
- `tomte.MaxCost(perRun, perDay)`: dollar limits; 0 means none. A run
  stops after the call that reaches its limit; a run doesn't start once
  the day's runs have spent the daily one.

A run that fails or stops at a limit says so through the channel, and
`run` exits with 1.

**Run log.** Each run adds one JSON line to `<name>.jsonl`, in
`$STATE_DIRECTORY` or the working directory (`tomte.Log(path)` to change
it): when, what started it, the task, tools called, tokens, cost, and the
result or error. The daily limit counts from it. `tomte.StateDir()` is
that directory, for an agent's own files.

```
tail -n 5 hn-digest.jsonl | jq '{start, trigger, tools, cost, result, error}'
```

## Deploying

An agent is one binary and an env file, run by systemd. The `tomte`
command does the copying over ssh. It needs Go 1.25 or newer where you
build, on its latest patch release since the agents are built with it,
and on the machine Linux with systemd 247 or newer (Debian 11, Ubuntu
22.04 or later) and an ssh user who may use sudo.

```sh
go install github.com/berkaycubuk/tomte/cmd/tomte@latest

tomte setup box                                   # once per machine
tomte deploy -env hn-digest.env box ./examples/hn-digest
tomte ls box
```

```
AGENT       STATE        MEMORY   NEXT RUN    LAST RUN               TODAY
hn-digest   active 3h    9.8M     Mon 08:00   2h ago, sent           1 run, $0.004 of $0.25
```

`box` is anything ssh takes: a host, `user@host` or an alias from
`~/.ssh/config`. Nothing runs on the machine but the agents.

**`tomte setup box`** asks for your sudo password once and installs:

- `tomte@.service`, the unit every agent runs with. Each agent runs as
  its own throwaway user (`DynamicUser`), can write only its state
  directory (`/var/lib/tomte/<name>/`, where the run log is), has no
  capabilities, devices or kernel settings, and only the system calls a
  service needs, and is capped at 128 MB of memory and half a CPU. An agent built with
  `tomte.ReadsJournal()` also joins the `systemd-journal` group, to read
  logs like sshd's; it's the only access an agent can ask for.
- `tomte.slice`, which all agents run in, so together they can't take
  more than `-memory` (1G) and `-cpu` (100%, one core) from whatever else
  runs on the machine.
- `/usr/local/libexec/tomte-helper`, a short script that does what needs
  root, and a sudoers rule that lets your ssh user run it, and only it,
  without a password. It takes only agent names, and touches only
  `tomte@<name>` (and its drop-ins), `/srv/tomte/<name>/` and
  `/var/lib/tomte/<name>/`.

Run it again to upgrade those or change the limits; agents are left
alone.

**`tomte deploy box ./package`** builds the agent for the machine's
architecture, uploads it, asks it its name (`./agent info`), puts it at
`/srv/tomte/<name>/<name>` and (re)starts `tomte@<name>`. The binary it
replaces is kept as `<name>.prev`. `-env file` uploads the agent's
variables to `/srv/tomte/<name>/env`; without it the env file on the
machine is kept:

```sh
MODEL_URL=https://openrouter.ai/api/v1
MODEL=z-ai/glm-4.6
MODEL_KEY=…
TK_TOKEN=tk_…
TZ=Europe/Istanbul
```

Runs in progress get 60 s to finish when an agent restarts.

**Day to day.** Flags go anywhere on the line.

```sh
tomte logs gandalf hn-digest            # the last 50 lines of its log; -f to follow, -n for more
tomte runs gandalf hn-digest            # its last 10 runs: when, trigger, cost, outcome; -json for jq
tomte run gandalf hn-digest "send the digest"   # one task now, waiting for it
tomte env gandalf hn-digest             # its variables, values hidden; -values to show them
tomte env -edit gandalf hn-digest       # change them in $EDITOR, then restart it
tomte env -set hn-digest.env gandalf hn-digest  # or replace them from a file
tomte rollback gandalf hn-digest        # back to the binary before the last deploy; again to undo
tomte rm gandalf hn-digest              # stop it and delete it; asks first, keeps its run log
tomte rm -purge -y gandalf hn-digest    # run log too, without asking
```

`tomte run` runs the agent as the service would, as its own user,
`tomte-<name>`, and inside the same limits. Its reply goes through its
channel, and the run is in the run log and `tomte runs` like any other.

## What's trusted with what

- **The ssh user** can deploy, configure, run and remove agents through
  `tomte-helper`, and nothing else as root. Since it deploys their code,
  it can do whatever an agent may do, reading the journal included with
  `tomte.ReadsJournal()`.
- **An agent** runs as its own user, in the sandbox above. It can reach
  the network, and write only its state directory.
- **Env files** hold an agent's secrets in plain text, readable by root
  and given only to that agent.
- **People writing to a bot** reach only its commands, which are Go code.
  A Telegram bot takes them from its own chat, which for a group means
  everyone in it; a `Public()` bot takes them from anyone. Nothing limits
  how often they write, and a command decides from `Request.From` what a
  sender may do, so a command that fetches a URL it's given fetches it for
  a stranger too.

See [SECURITY.md](SECURITY.md) to report a vulnerability.

## License

MIT. See [LICENSE](LICENSE).
