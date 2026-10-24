package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	pos, err := parse(fs, args, 3, true, "<box> <agent> <task>")
	if err != nil {
		return err
	}
	b, name, task := box(pos[0]), pos[1], strings.Join(pos[2:], " ")

	// The agent's log lines come as it works; its reply goes through its
	// channel, as from a schedule, and is shown from the run log after.
	runErr := b.helper(nil, os.Stdout, "run", name, task)
	recs, err := lastRuns(b, name, 1)
	// An agent that stopped before running, or a run from a schedule that
	// ended meanwhile, has no record of this run to show.
	if err != nil || len(recs) == 0 || recs[0].Trigger != "cli" ||
		!strings.HasPrefix(task, strings.TrimSuffix(recs[0].Task, "…")) {
		return runErr
	}
	r := recs[0]
	fmt.Printf("\n%s: %s, %d steps, $%.3f", name, r.Took, r.Steps, r.Cost)
	if len(r.Tools) > 0 {
		fmt.Printf(", tools: %s", strings.Join(r.Tools, ", "))
	}
	fmt.Printf("\n\n%s\n", r.outcome())
	if runErr != nil {
		return fmt.Errorf("%s failed", name)
	}
	return nil
}
