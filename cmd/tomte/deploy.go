package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// nameRe is the agent names tomte-helper takes: they become a unit name
// and directory names on the box.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func deploy(args []string) error {
	fs := flag.NewFlagSet("deploy", flag.ContinueOnError)
	envFile := fs.String("env", "", "the agent's environment `file`, replacing the one on the box")
	if err := parse(fs, args, 2, "<box> <package>"); err != nil {
		return err
	}
	b, pkg := box(fs.Arg(0)), fs.Arg(1)
	var env []byte
	if *envFile != "" {
		var err error
		if env, err = os.ReadFile(*envFile); err != nil {
			return err
		}
	}

	machine, err := b.output("uname -m")
	if err != nil {
		return err
	}
	goarch, goarm, err := goArch(machine)
	if err != nil {
		return fmt.Errorf("%s: %w", b, err)
	}
	dir, err := os.MkdirTemp("", "tomte-deploy-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	bin := filepath.Join(dir, "agent")
	fmt.Fprintf(os.Stderr, "building %s for linux/%s\n", pkg, goarch)
	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin, pkg)
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+goarch, "GOARM="+goarm)
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("go build: %w", err)
	}

	// The binary goes up under a random id and says what it's called;
	// only then does it take the place of the agent of that name.
	id := make([]byte, 8)
	rand.Read(id)
	upload := hex.EncodeToString(id)
	f, err := os.Open(bin)
	if err != nil {
		return err
	}
	defer f.Close()
	var out bytes.Buffer
	if err := b.helper(f, &out, "stage", upload); err != nil {
		return err
	}
	var in agentInfo
	if err := json.Unmarshal(out.Bytes(), &in); err != nil {
		return fmt.Errorf("reading the agent's info: %w (it said %q)", err, out.String())
	}
	if err := in.check(); err != nil {
		return err
	}

	if env != nil {
		if err := b.helper(bytes.NewReader(env), nil, "env", in.Name); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "starting %s on %s\n", in.Name, b)
	out.Reset()
	if err := b.helper(nil, &out, "activate", upload, in.Name); err != nil {
		return err
	}
	// Described again with its env file, the agent's next run is in its
	// own time zone.
	json.Unmarshal(out.Bytes(), &in)
	fmt.Printf("%s is running on %s; next run %s\n", in.Name, b, in.next().Format("Mon 2 Jan 15:04 MST"))
	return nil
}

// check refuses an agent that couldn't serve.
func (in agentInfo) check() error {
	if !nameRe.MatchString(in.Name) {
		return fmt.Errorf("agent name %q: deployed agents need a name of up to 64 lowercase letters, digits and dashes", in.Name)
	}
	if len(in.Errors) > 0 {
		return fmt.Errorf("%s has mistakes in its options:\n  %s", in.Name, strings.Join(in.Errors, "\n  "))
	}
	if len(in.Schedules) == 0 {
		return fmt.Errorf("%s has no schedule, so it has nothing to do while serving: add tomte.Cron", in.Name)
	}
	return nil
}

// next is when the agent's soonest schedule is due.
func (in agentInfo) next() time.Time {
	var t time.Time
	for _, s := range in.Schedules {
		if t.IsZero() || s.Next.Before(t) {
			t = s.Next
		}
	}
	return t
}

// goArch is the Go architecture for what `uname -m` says.
func goArch(machine string) (goarch, goarm string, err error) {
	switch machine {
	case "x86_64", "amd64":
		return "amd64", "", nil
	case "aarch64", "arm64":
		return "arm64", "", nil
	case "armv7l", "armv8l":
		return "arm", "7", nil
	case "armv6l":
		return "arm", "6", nil
	case "i386", "i686":
		return "386", "", nil
	case "riscv64":
		return "riscv64", "", nil
	}
	return "", "", errors.New("no Go architecture for machine " + machine)
}
