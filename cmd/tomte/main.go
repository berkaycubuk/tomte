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
  tomte setup [-memory 1G] [-cpu 100%] <box>   set a machine up for agents
  tomte deploy [-env file] <box> <package>     build an agent and (re)start it there
  tomte ls <box>                               the agents there and how they're doing
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
	}
	cmd, ok := commands[os.Args[1]]
	if !ok {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err := cmd(os.Args[2:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintf(os.Stderr, "tomte %s: %v\n", os.Args[1], err)
		}
		os.Exit(1)
	}
}

// parse parses a command's flags and checks it got n arguments.
func parse(fs *flag.FlagSet, args []string, n int, names string) error {
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "usage: tomte %s [flags] %s\n", fs.Name(), names)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != n {
		fs.Usage()
		return flag.ErrHelp
	}
	return nil
}
