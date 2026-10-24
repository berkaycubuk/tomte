package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"text/template"
)

var (
	//go:embed box/setup.sh
	setupScript string
	//go:embed box/tomte@.service
	unitFile string
	//go:embed box/tomte.slice
	sliceFile string
	//go:embed box/tomte-helper
	helperScript string
)

var (
	memoryRe = regexp.MustCompile(`^[0-9]+[KMGT]?$`)
	cpuRe    = regexp.MustCompile(`^[0-9]+%$`)
)

func setup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	memory := fs.String("memory", "1G", "memory all agents together may use")
	cpu := fs.String("cpu", "100%", "CPU all agents together may use; 100% is one core")
	pos, err := parse(fs, args, 1, false, "<box>")
	if err != nil {
		return err
	}
	if !memoryRe.MatchString(*memory) {
		return fmt.Errorf("-memory %q: give bytes, or a number with K, M, G or T", *memory)
	}
	if !cpuRe.MatchString(*cpu) {
		return fmt.Errorf("-cpu %q: give a percentage, like 150%%", *cpu)
	}
	script, err := setupFor(*memory, *cpu)
	if err != nil {
		return err
	}
	// The script travels in the command, so the terminal stays free for
	// sudo to ask for a password.
	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	remote := `s=$(echo ` + b64 + ` | base64 -d) && if [ "$(id -u)" = 0 ]; then sh -c "$s"; else sudo sh -c "$s"; fi`
	cmd := exec.Command("ssh", "-t", pos[0], remote)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("on %s: %w", pos[0], err)
	}
	return nil
}

// setupFor is box/setup.sh with the files it installs written into it.
func setupFor(memory, cpu string) (string, error) {
	limits := struct{ Memory, CPU string }{memory, cpu}
	slice, err := render(sliceFile, limits)
	if err != nil {
		return "", err
	}
	return render(setupScript, map[string]string{
		"Unit":   unitFile,
		"Slice":  slice,
		"Helper": helperScript,
		"Memory": memory,
		"CPU":    cpu,
	})
}

func render(text string, data any) (string, error) {
	t, err := template.New("").Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	err = t.Execute(&b, data)
	return b.String(), err
}
