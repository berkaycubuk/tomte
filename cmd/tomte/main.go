// Command tomte deploys and watches Tomte agents on a machine over ssh.
//
//	tomte setup box                  once per machine: the unit, the slice and the helper
//	tomte deploy box ./my-agent      build, upload and (re)start an agent
//	tomte ls box                     every agent on the machine and how it's doing
//
// box is anything ssh takes: a host, user@host or an alias from
// ~/.ssh/config. Nothing runs on the machine besides the agents: systemd
// keeps them going, and tomte reaches them through a small helper script
// that `tomte setup` installs and that the ssh user may run as root.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

const usage = `usage:
  tomte setup [-memory 1G] [-cpu 100%] <box>     set a machine up for agents
  tomte deploy [-env file] <box> <package>       build an agent and (re)start it there
  tomte ls <box>                                 the agents there and how they're doing
  tomte env [-values | -edit | -set file] <box> <agent>
                                                 show or change an agent's environment
  tomte logs [-f] [-n 50] <box> <agent>          an agent's log
  tomte runs [-n 10] [-json] <box> <agent>       an agent's last runs
  tomte run <box> <agent> <task>                 do one task now and wait for it
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	commands := map[string]func([]string) error{
		"setup":  setup,
		"deploy": deploy,
		"ls":     ls,
		"env":    env,
		"logs":   logs,
		"runs":   runs,
		"run":    run,
	}
	cmd, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) && !errors.Is(err, errShown) {
			fmt.Fprintf(os.Stderr, "tomte %s: %v\n", os.Args[1], err)
		}
		os.Exit(1)
	}
}

// parse parses a command's flags, which may come before, between or
// after its arguments, and returns the arguments: n of them, or with
// rest, at least n, where everything after the nth is an argument even
// if it looks like a flag.
func parse(fs *flag.FlagSet, args []string, n int, rest bool, names string) ([]string, error) {
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: tomte %s [flags] %s\n", fs.Name(), names)
		fs.PrintDefaults()
	}
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 || rest && len(pos) == n {
			pos = append(pos, args...)
			break
		}
		pos, args = append(pos, args[0]), args[1:]
	}
	if len(pos) < n || !rest && len(pos) > n {
		fs.Usage()
		return nil, flag.ErrHelp
	}
	return pos, nil
}
