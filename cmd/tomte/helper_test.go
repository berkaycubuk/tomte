package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBox runs box/tomte-helper with its paths moved into a temporary
// directory and systemd replaced by stubs that note what they're asked.
type fakeBox struct {
	t                *testing.T
	dir, root, state string
	helper           string
	path             string
}

func newFakeBox(t *testing.T) *fakeBox {
	dir := t.TempDir()
	b := &fakeBox{t: t, dir: dir, root: filepath.Join(dir, "srv"), state: filepath.Join(dir, "state")}
	script := strings.NewReplacer(
		"root=/srv/tomte", "root="+b.root,
		"state=/var/lib/tomte", "state="+b.state,
	).Replace(helperScript)
	b.helper = filepath.Join(dir, "tomte-helper")
	b.write(b.helper, script, 0o755)

	bin := filepath.Join(dir, "bin")
	stubs := map[string]string{
		// systemd-run … <binary> info: run the binary.
		"systemd-run": `eval "b=\${$(($# - 1))}"; exec "$b" info`,
		"systemctl": `echo "systemctl $*" >>"$CALLS"
case $1 in
show) printf 'ActiveState=active\nSubState=running\nMemoryCurrent=10485760\nActiveEnterTimestamp=@1000\n' ;;
is-active) [ ! -e "$DIR/failing" ] ;;
esac`,
		"journalctl": `echo "it broke"`,
		"sleep":      `:`,
		"date":       `case $1 in +%s) echo 4600 ;; +%F) echo 2026-10-24 ;; esac`,
	}
	for name, body := range stubs {
		b.write(filepath.Join(bin, name), "#!/bin/sh\n"+body+"\n", 0o755)
	}
	b.path = bin + ":" + os.Getenv("PATH")
	return b
}

func (b *fakeBox) write(path, content string, mode os.FileMode) {
	b.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		b.t.Fatal(err)
	}
}

func (b *fakeBox) run(stdin string, args ...string) (string, error) {
	b.t.Helper()
	cmd := exec.Command("sh", append([]string{b.helper}, args...)...)
	cmd.Env = append(os.Environ(), "PATH="+b.path, "CALLS="+filepath.Join(b.dir, "calls"), "DIR="+b.dir)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (b *fakeBox) calls() string {
	c, _ := os.ReadFile(filepath.Join(b.dir, "calls"))
	return string(c)
}

// agent is a stand-in agent binary that describes itself as name.
func agent(name string) string {
	return "#!/bin/sh\necho '{\"name\":\"" + name + "\",\"schedules\":[{\"spec\":\"@daily\",\"next\":\"2026-10-25T00:00:00Z\"}]}'\n"
}

func TestHelperDeploys(t *testing.T) {
	b := newFakeBox(t)
	out, err := b.run(agent("digest"), "stage", "abc123")
	if err != nil || !strings.Contains(out, `"name":"digest"`) {
		t.Fatalf("stage: %v\n%s", err, out)
	}
	if out, err := b.run("KEY=1\n", "env", "digest"); err != nil {
		t.Fatalf("env: %v\n%s", err, out)
	}
	if out, err := b.run("", "activate", "abc123", "digest"); err != nil || !strings.Contains(out, `"name":"digest"`) {
		t.Fatalf("activate: %v\n%s", err, out)
	}
	d := filepath.Join(b.root, "digest")
	if fi, err := os.Stat(filepath.Join(d, "env")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("env file: %v %v", fi.Mode(), err)
	}
	if _, err := os.Stat(filepath.Join(d, "digest")); err != nil {
		t.Error(err)
	}
	if c := b.calls(); !strings.Contains(c, "systemctl enable --quiet tomte@digest") || !strings.Contains(c, "systemctl restart tomte@digest") {
		t.Errorf("calls:\n%s", c)
	}

	// A second deploy keeps the first binary to go back to.
	b.run(agent("digest")+"# v2\n", "stage", "def456")
	if out, err := b.run("", "activate", "def456", "digest"); err != nil {
		t.Fatalf("activate: %v\n%s", err, out)
	}
	if prev, _ := os.ReadFile(filepath.Join(d, "digest.prev")); string(prev) != agent("digest") {
		t.Errorf("digest.prev = %q", prev)
	}
}

func TestHelperReportsAnAgentThatWontStart(t *testing.T) {
	b := newFakeBox(t)
	b.write(filepath.Join(b.dir, "failing"), "", 0o644)
	b.run(agent("digest"), "stage", "abc")
	out, err := b.run("", "activate", "abc", "digest")
	if err == nil || !strings.Contains(out, "it broke") {
		t.Errorf("activate: %v\n%s", err, out)
	}
}

func TestHelperRefusesNonAgents(t *testing.T) {
	b := newFakeBox(t)
	out, err := b.run("#!/bin/sh\nexit 2\n", "stage", "abc")
	if err == nil || !strings.Contains(out, "didn't describe itself") {
		t.Errorf("stage: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(b.root, ".incoming", "abc")); err == nil {
		t.Error("the upload was kept")
	}
}

// The helper runs as root for anyone with ssh access, so it takes only
// plain agent names and ids.
func TestHelperRefusesBadNames(t *testing.T) {
	b := newFakeBox(t)
	bad := [][]string{
		{"env", "../etc"},
		{"env", "-rf"},
		{"env", "Digest"},
		{"env", ""},
		{"env", strings.Repeat("a", 65)},
		{"activate", "abc", "a/b"},
		{"activate", "../x", "digest"},
		{"stage", "abc/../../x"},
		{"env", "digest", "extra"},
		{"rm", "digest"},
	}
	for _, args := range bad {
		if out, err := b.run("", args...); err == nil {
			t.Errorf("%q was accepted:\n%s", args, out)
		}
	}
	if c := b.calls(); c != "" {
		t.Errorf("calls:\n%s", c)
	}
	if entries, _ := os.ReadDir(b.root); len(entries) > 0 {
		t.Errorf("wrote %v", entries)
	}
}

func TestHelperLists(t *testing.T) {
	b := newFakeBox(t)
	b.run(agent("digest"), "stage", "abc")
	b.run("", "activate", "abc", "digest")
	b.write(filepath.Join(b.state, "digest", "digest.jsonl"),
		`{"start":"2026-10-23T08:00:00Z","cost":0.5,"result":"old"}`+"\n"+
			`{"start":"2026-10-24T08:00:00Z","cost":0.01,"result":"hi"}`+"\n", 0o600)
	b.write(filepath.Join(b.root, "Not An Agent", "x"), "", 0o755)

	out, err := b.run("", "ls")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	now, agents, err := parseList(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if now.Unix() != 4600 || len(agents) != 1 {
		t.Fatalf("now %v, agents %+v\n%s", now, agents, out)
	}
	a := agents[0]
	if a.name != "digest" || a.info == nil || len(a.today) != 1 || a.last == nil || a.last.Result != "hi" {
		t.Errorf("agent = %+v\n%s", a, out)
	}
	if s := a.state(now); s != "active 1h" {
		t.Errorf("state = %q", s)
	}
	if m := a.memory(); m != "10.0M" {
		t.Errorf("memory = %q", m)
	}
}
