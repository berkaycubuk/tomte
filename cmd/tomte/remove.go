package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

func rollback(args []string) error {
	fs := flag.NewFlagSet("rollback", flag.ContinueOnError)
	pos, err := parse(fs, args, 2, false, "<box> <agent>")
	if err != nil {
		return err
	}
	b, name := box(pos[0]), pos[1]
	fmt.Fprintf(os.Stderr, "restarting %s with its previous binary\n", name)
	var out bytes.Buffer
	if err := b.helper(nil, &out, "rollback", name); err != nil {
		return err
	}
	fmt.Printf("%s is back on its previous binary on %s", name, b)
	var in agentInfo
	if json.Unmarshal(out.Bytes(), &in) == nil && !in.next().IsZero() {
		fmt.Printf("; next run %s", in.next().Format("Mon 2 Jan 15:04 MST"))
	}
	fmt.Printf("\n`tomte rollback %s %s` again undoes this.\n", b, name)
	return nil
}

func rm(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	purge := fs.Bool("purge", false, "delete its run log too")
	yes := fs.Bool("y", false, "don't ask first")
	pos, err := parse(fs, args, 2, false, "<box> <agent>")
	if err != nil {
		return err
	}
	b, name := box(pos[0]), pos[1]

	if !*yes {
		if !isTerminal(os.Stdin) {
			return errors.New("not asking without a terminal: add -y to remove it")
		}
		keeps := "Its run log stays (-purge deletes it)."
		if *purge {
			keeps = "Its run log goes too."
		}
		fmt.Printf("This stops %s on %s and deletes its binary and env file. %s\nType its name to go ahead: ", name, b, keeps)
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(answer) != name {
			return errors.New("left as it was")
		}
	}

	verb := []string{"rm", name}
	if *purge {
		verb = append(verb, "purge")
	}
	if err := b.helper(nil, nil, verb...); err != nil {
		return err
	}
	fmt.Printf("%s is gone from %s\n", name, b)
	return nil
}
