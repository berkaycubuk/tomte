# Tomte

Small agents that do one job, in Go.

A tomte is the farm spirit that quietly does the chores at night. A Tomte
agent is a Go program that does one task, on a schedule or when asked, and
compiles to one small binary: about 6 MB, and about 11 MB of memory while
it waits. Dozens fit on a small machine.

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

**Channel.** `tomte.SendTo(channel)` sets where results and failures go;
it's stdout if not given. `channels/telegram` sends to a Telegram chat. A
channel is two methods, `Name()` and `Send(ctx, Message)`, so writing your
own is short.

**Limits.** Nobody watches a scheduled run, so:

- `tomte.MaxSteps(n)`: model calls per run, 20 if not given.
- `tomte.Price(in, out)`: the model's price in dollars per million tokens.
- `tomte.MaxCost(perRun, perDay)`: dollar limits; 0 means none. A run
  stops after the call that reaches its limit; a run doesn't start once
  the day's runs have spent the daily one.

A run that fails or stops at a limit says so through the channel, and
`run` exits with 1.

**Run log.** Each run adds one JSON line to `<name>.jsonl`, in
`$STATE_DIRECTORY` or the working directory (`tomte.Log(path)` to change
it): when, what started it, the task, tools called, tokens, cost, and the
result or error. The daily limit counts from it.

```
tail -n 5 hn-digest.jsonl | jq '{start, trigger, tools, cost, result, error}'
```

## Deploying

An agent is one binary and an env file, run by systemd. The `tomte`
command does the copying over ssh:

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
  directory (`/var/lib/tomte/<name>/`, where the run log is), and is
  capped at 128 MB of memory and half a CPU.
- `tomte.slice`, which all agents run in, so together they can't take
  more than `-memory` (1G) and `-cpu` (100%, one core) from whatever else
  runs on the machine.
- `/usr/local/libexec/tomte-helper`, a short script that does what needs
  root, and a sudoers rule that lets your ssh user run it, and only it,
  without a password. It takes only agent names, and touches only
  `tomte@<name>`, `/srv/tomte/<name>/` and `/var/lib/tomte/<name>/`.

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
TELEGRAM_TOKEN=…
TELEGRAM_CHAT=…
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
