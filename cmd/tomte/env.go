package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func env(args []string) error {
	fs := flag.NewFlagSet("env", flag.ContinueOnError)
	values := fs.Bool("values", false, "show the values too, secrets included")
	edit := fs.Bool("edit", false, "edit the env file in $EDITOR, then restart the agent")
	set := fs.String("set", "", "replace the env file with this `file`, then restart the agent")
	pos, err := parse(fs, args, 2, false, "<box> <agent>")
	if err != nil {
		return err
	}
	b, name := box(pos[0]), pos[1]
	if *edit && *set != "" {
		return errors.New("-edit or -set, not both")
	}

	if *set != "" {
		content, err := os.ReadFile(*set)
		if err != nil {
			return err
		}
		return replaceEnv(b, name, content)
	}

	var current bytes.Buffer
	if err := b.helper(nil, &current, "getenv", name); err != nil {
		return err
	}
	if !*edit {
		if *values {
			os.Stdout.Write(current.Bytes())
		} else {
			fmt.Print(hideValues(current.String()))
		}
		return nil
	}

	edited, err := editFile(current.Bytes())
	if err != nil {
		return err
	}
	if bytes.Equal(edited, current.Bytes()) {
		fmt.Println("no changes")
		return nil
	}
	return replaceEnv(b, name, edited)
}

// replaceEnv uploads an agent's env file and restarts it, since the agent
// reads its environment only when it starts.
func replaceEnv(b box, name string, content []byte) error {
	if err := b.helper(bytes.NewReader(content), nil, "env", name); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "restarting %s\n", name)
	if err := b.helper(nil, nil, "restart", name); err != nil {
		return err
	}
	fmt.Printf("%s is running with its new environment\n", name)
	return nil
}

// hideValues keeps an env file's names and comments and hides its values.
func hideValues(env string) string {
	var b strings.Builder
	for line := range strings.Lines(env) {
		if k, v, ok := strings.Cut(line, "="); ok && !strings.HasPrefix(strings.TrimSpace(k), "#") {
			if strings.TrimSpace(v) == "" {
				line = k + "=\n"
			} else {
				line = k + "=…\n"
			}
		}
		b.WriteString(line)
	}
	return b.String()
}

// editFile opens content in $EDITOR (vi if unset) and returns what the
// file holds when the editor exits. The file is the user's alone and is
// removed afterwards.
func editFile(content []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "tomte-env-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "env")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return nil, err
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	// Through the shell, so EDITOR may carry arguments ("code --wait").
	cmd := exec.Command("sh", "-c", editor+` "$1"`, "sh", path)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w; nothing changed", editor, err)
	}
	return os.ReadFile(path)
}
