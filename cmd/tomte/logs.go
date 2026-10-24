package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
)

func logs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := fs.Bool("f", false, "keep printing new lines until Ctrl-C")
	n := fs.Int("n", 50, "how many lines")
	pos, err := parse(fs, args, 2, false, "<box> <agent>")
	if err != nil {
		return err
	}
	if *n < 0 {
		return fmt.Errorf("-n %d: give a count", *n)
	}
	b, name := box(pos[0]), pos[1]
	if *follow {
		return b.helperTerm("logs", name, strconv.Itoa(*n), "follow")
	}
	return b.helper(nil, os.Stdout, "logs", name, strconv.Itoa(*n))
}

func runs(args []string) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	n := fs.Int("n", 10, "how many runs")
	raw := fs.Bool("json", false, "print the run log's lines as they are, for jq")
	pos, err := parse(fs, args, 2, false, "<box> <agent>")
	if err != nil {
		return err
	}
	if *n < 1 {
		return fmt.Errorf("-n %d: give a count", *n)
	}
	b, name := box(pos[0]), pos[1]
	if *raw {
		return b.helper(nil, os.Stdout, "runs", name, strconv.Itoa(*n))
	}
	recs, err := lastRuns(b, name, *n)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		fmt.Printf("%s hasn't run yet\n", name)
		return nil
	}
	printRuns(os.Stdout, recs)
	return nil
}

// lastRuns reads the last n lines of an agent's run log, oldest first.
func lastRuns(b box, name string, n int) ([]runRecord, error) {
	var out bytes.Buffer
	if err := b.helper(nil, &out, "runs", name, strconv.Itoa(n)); err != nil {
		return nil, err
	}
	var recs []runRecord
	for line := range strings.Lines(out.String()) {
		var r runRecord
		if json.Unmarshal([]byte(line), &r) == nil {
			recs = append(recs, r)
		}
	}
	return recs, nil
}

func printRuns(w io.Writer, recs []runRecord) {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tTRIGGER\tTOOK\tSTEPS\tCOST\tOUTCOME")
	for _, r := range recs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t$%.3f\t%s\n",
			r.Start.Format("Mon 2 Jan 15:04"), r.Trigger, r.Took, r.Steps, r.Cost, cutLine(r.outcome(), 70))
	}
	tw.Flush()
}

// outcome is how a run ended: its error, or its reply.
func (r runRecord) outcome() string {
	switch {
	case r.Error != "":
		return "failed: " + r.Error
	case r.Result == "NOTHING":
		return "nothing to say"
	}
	return r.Result
}

// cutLine is s's first line, at most n characters long.
func cutLine(s string, n int) string {
	s, _, more := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	if more {
		return s + " …"
	}
	return s
}
