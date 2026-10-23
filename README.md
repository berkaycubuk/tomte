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

An agent is one binary and an env file. On a Linux machine with systemd:

```sh
# build for the machine (arm64 for a Raspberry Pi)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o hn-digest ./examples/hn-digest

# copy it, with its settings
ssh box 'sudo mkdir -p /srv/tomte/hn-digest'
scp hn-digest box:/tmp/ && ssh box 'sudo mv /tmp/hn-digest /srv/tomte/hn-digest/'
ssh box 'sudo tee /srv/tomte/hn-digest/env >/dev/null && sudo chmod 600 /srv/tomte/hn-digest/env' <<'EOF'
MODEL_URL=https://openrouter.ai/api/v1
MODEL=z-ai/glm-4.6
MODEL_KEY=…
TELEGRAM_TOKEN=…
TELEGRAM_CHAT=…
TZ=Europe/Istanbul
EOF

# once per machine: the unit every agent shares
scp deploy/tomte@.service box:/tmp/ && ssh box 'sudo mv /tmp/tomte@.service /etc/systemd/system/ && sudo systemctl daemon-reload'

ssh box 'sudo systemctl enable --now tomte@hn-digest'
```

Each agent runs as its own throwaway user (`DynamicUser`), can write only
its state directory (`/var/lib/tomte/<name>/`, where the run log is), and
is capped at 128 MB of memory and half a CPU. Logs are in
`journalctl -u tomte@hn-digest`. To update, copy the new binary and
`systemctl restart tomte@hn-digest`; runs in progress get 60 s to finish.

To try a deployed agent by hand:

```sh
sudo systemd-run --pty -p DynamicUser=yes -p StateDirectory=tomte/hn-digest \
  -p EnvironmentFile=/srv/tomte/hn-digest/env /srv/tomte/hn-digest/hn-digest run "send the digest"
```
