package main

// Reaching the machine: everything goes through ssh, and everything that
// needs root goes through tomte-helper (box/tomte-helper).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type box string

// run runs a shell command on the box without a terminal; ssh never asks
// for a password then, it fails instead. What the command prints on
// stderr is shown as it comes.
func (b box) run(stdin io.Reader, stdout io.Writer, command string) error {
	return b.ssh(stdin, stdout, command, false)
}

func (b box) ssh(stdin io.Reader, stdout io.Writer, command string, term bool) error {
	args := []string{"-o", "BatchMode=yes"}
	if term {
		args = append(args, "-t")
	}
	var stderr bytes.Buffer
	cmd := exec.Command("ssh", append(args, string(b), command)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, io.MultiWriter(os.Stderr, &stderr)
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		switch {
		case errors.As(err, &exit) && exit.ExitCode() == notSetUp,
			strings.Contains(stderr.String(), "sudo: a password is required"):
			return fmt.Errorf("%s isn't set up for tomte: run `tomte setup %s` first", b, b)
		case strings.Contains(stderr.String(), "tomte-helper: unknown command"),
			strings.Contains(stderr.String(), "tomte-helper: usage: tomte-helper"): // helpers from before env
			return fmt.Errorf("the helper on %s is older than this tomte command: run `tomte setup %s` to upgrade it", b, b)
		case strings.Contains(stderr.String(), "tomte-helper: "):
			return errShown
		}
		return fmt.Errorf("on %s: %w", b, err)
	}
	return nil
}

// errShown is a failure the helper has already explained on stderr.
var errShown = errors.New("failed")

const helperPath = "/usr/local/libexec/tomte-helper"

// notSetUp is the exit code when the box has no helper.
const notSetUp = 97

// helper runs tomte-helper on the box as root.
func (b box) helper(stdin io.Reader, stdout io.Writer, args ...string) error {
	return b.run(stdin, stdout, helperCommand(args))
}

// helperTerm runs tomte-helper on the box in a terminal, so that Ctrl-C
// reaches it; for commands that run until stopped.
func (b box) helperTerm(args ...string) error {
	return b.ssh(os.Stdin, os.Stdout, helperCommand(args), isTerminal(os.Stdin))
}

func helperCommand(args []string) string {
	return fmt.Sprintf(`h=%s; [ -x $h ] || exit %d; [ "$(id -u)" = 0 ] || h="sudo -n $h"; exec $h %s`,
		helperPath, notSetUp, quote(args...))
}

// isTerminal says whether f is a terminal, near enough without x/term: a
// character device that isn't /dev/null, the one stdin is often given.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	null, err := os.Stat(os.DevNull)
	return err != nil || !os.SameFile(fi, null)
}

// output runs a shell command on the box and returns what it printed.
func (b box) output(command string) (string, error) {
	var out bytes.Buffer
	err := b.run(nil, &out, command)
	return strings.TrimSpace(out.String()), err
}

// quote makes words safe to pass through the remote shell.
func quote(words ...string) string {
	q := make([]string, len(words))
	for i, w := range words {
		q[i] = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
	}
	return strings.Join(q, " ")
}
