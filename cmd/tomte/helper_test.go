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
	private          string
	helper           string
	path             string
}

func newFakeBox(t *testing.T) *fakeBox {
	dir := t.TempDir()
	b := &fakeBox{t: t, dir: dir, root: filepath.Join(dir, "srv"), state: filepath.Join(dir, "state"), private: filepath.Join(dir, "private")}
	script := strings.NewReplacer(
		"root=/srv/tomte", "root="+b.root,
		"state=/var/lib/tomte", "state="+b.state,
		"private=/var/lib/private/tomte", "private="+b.private,
		"units=/etc/systemd/system", "units="+filepath.Join(dir, "units"),
	).Replace(helperScript)
	b.helper = filepath.Join(dir, "tomte-helper")
	b.write(b.helper, script, 0o755)

	bin := filepath.Join(dir, "bin")
	stubs := map[string]string{
		// systemd-run [options] <command>: note the options, load the env
		// file, run the command.
		"systemd-run": `echo "systemd-run $*" >>"$CALLS"
while [ $# -gt 0 ]; do
	case $1 in
	/*) break ;;
	EnvironmentFile=-*) f=${1#EnvironmentFile=-}; if [ -e "$f" ]; then set -a; . "$f"; set +a; fi ;;
	esac
	shift
done
exec "$@"`,
		"systemctl": `echo "systemctl $*" >>"$CALLS"
case $1 in
show) printf 'ActiveState=active\nSubState=running\nMemoryCurrent=10485760\nActiveEnterTimestamp=@1000\n' ;;
is-active) [ ! -e "$DIR/failing" ] ;;
esac`,
		"journalctl": `echo "journalctl $*" >>"$CALLS"; echo "it broke"`,
		"sleep":      `:`,
		// It's past midnight in Tokyo.
		"date": `case $1 in +%s) echo 4600 ;; +%F) if [ "${TZ-}" = Asia/Tokyo ]; then echo 2026-10-25; else echo 2026-10-24; fi ;; esac`,
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

// agent is a stand-in agent binary that describes itself as name, and
// says what it was asked to do otherwise.
func agent(name string) string {
	return "#!/bin/sh\n[ \"$1\" = run ] && { echo \"did: $2\"; exit 0; }\n" +
		"echo '{\"name\":\"" + name + "\",\"schedules\":[{\"spec\":\"@daily\",\"next\":\"2026-10-25T00:00:00Z\"}]}'\n"
}

// deployed is a fake box with one agent on it.
func deployed(t *testing.T, name string) *fakeBox {
	b := newFakeBox(t)
	if out, err := b.run(agent(name), "stage", "abc"); err != nil {
		t.Fatalf("stage: %v\n%s", err, out)
	}
	if out, err := b.run("", "activate", "abc", name); err != nil {
		t.Fatalf("activate: %v\n%s", err, out)
	}
	os.Remove(filepath.Join(b.dir, "calls"))
	return b
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
	// A binary describing itself mustn't hang the deploy.
	if c := b.calls(); !strings.Contains(c, "systemd-run --quiet --wait --pipe --collect -p RuntimeMaxSec=30 -p MemoryMax=128M") {
		t.Errorf("describe has no limits:\n%s", c)
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
		{"env", strings.Repeat("a", 26)},
		{"activate", "abc", "a/b"},
		{"activate", "../x", "digest"},
		{"stage", "abc/../../x"},
		{"env", "digest", "extra"},
		{"getenv", "../digest"},
		{"restart", "-digest"},
		{"logs", "digest", "5; rm"},
		{"logs", "digest", "-1"},
		{"runs", "a b", "5"},
		{"runs", "digest", ""},
		{"run", "../x", "task"},
		{"rollback", "../digest"},
		{"rm", ".."},
		{"rm", ""},
		{"rm", "digest", "purge", "now"},
		{"frobnicate", "digest"},
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

func TestHelperEnv(t *testing.T) {
	b := deployed(t, "digest")
	if out, err := b.run("A=1\nB=2\n", "env", "digest"); err != nil {
		t.Fatalf("env: %v\n%s", err, out)
	}
	if out, err := b.run("", "getenv", "digest"); err != nil || out != "A=1\nB=2\n" {
		t.Errorf("getenv: %v %q", err, out)
	}
	if out, err := b.run("", "restart", "digest"); err != nil || !strings.Contains(b.calls(), "systemctl restart tomte@digest") {
		t.Errorf("restart: %v\n%s\n%s", err, out, b.calls())
	}
	if out, err := b.run("", "getenv", "nobody"); err == nil || !strings.Contains(out, "no agent named nobody") {
		t.Errorf("getenv of a missing agent: %v %q", err, out)
	}
}

func TestHelperLogsAndRuns(t *testing.T) {
	b := deployed(t, "digest")
	b.run("", "logs", "digest", "20")
	b.run("", "logs", "digest", "5", "follow")
	c := b.calls()
	if !strings.Contains(c, "journalctl -u tomte@digest -n 20 -o short-iso --no-pager") ||
		!strings.Contains(c, "journalctl -u tomte@digest -n 5 -o short-iso -f") {
		t.Errorf("calls:\n%s", c)
	}

	if out, err := b.run("", "runs", "digest", "5"); err != nil || out != "" {
		t.Errorf("runs before any: %v %q", err, out)
	}
	var log strings.Builder
	for i := range 5 {
		log.WriteString(`{"start":"2026-10-24T08:00:0` + string(rune('0'+i)) + `Z"}` + "\n")
	}
	b.write(filepath.Join(b.state, "digest", "digest.jsonl"), log.String(), 0o600)
	if out, _ := b.run("", "runs", "digest", "2"); strings.Count(out, "\n") != 2 || !strings.Contains(out, "08:00:04") {
		t.Errorf("runs: %q", out)
	}
}

// An agent's runs are logged in its own time zone, and so is its today.
func TestHelperListsTodayInTheAgentsTimeZone(t *testing.T) {
	b := deployed(t, "digest")
	b.run("TZ=Asia/Tokyo\n", "env", "digest")
	b.write(filepath.Join(b.state, "digest", "digest.jsonl"),
		`{"start":"2026-10-24T23:00:00+09:00","cost":0.5}`+"\n"+
			`{"start":"2026-10-25T08:00:00+09:00","cost":0.01}`+"\n", 0o600)
	out, err := b.run("", "ls")
	if err != nil {
		t.Fatalf("ls: %v\n%s", err, out)
	}
	_, agents, err := parseList(strings.NewReader(out))
	if err != nil || len(agents) != 1 || len(agents[0].today) != 1 || agents[0].today[0].Cost != 0.01 {
		t.Errorf("agents %+v, %v\n%s", agents, err, out)
	}
}

// The agent writes its state directory, so a run log there may be a
// symlink to anything; root mustn't be the one reading it.
func TestHelperReadsRunLogsAsTheAgent(t *testing.T) {
	b := deployed(t, "digest")
	b.write(filepath.Join(b.state, "digest", "digest.jsonl"), `{"start":"2026-10-24T08:00:00Z"}`+"\n", 0o600)
	asAgent := "-p DynamicUser=yes -p User=tomte-digest -p StateDirectory=tomte/digest -p PrivateNetwork=yes"
	for _, args := range [][]string{{"runs", "digest", "1"}, {"ls"}} {
		os.Remove(filepath.Join(b.dir, "calls"))
		if out, err := b.run("", args...); err != nil || !strings.Contains(out, "08:00:00") {
			t.Errorf("%s: %v %q", args[0], err, out)
		}
		if c := b.calls(); !strings.Contains(c, asAgent) {
			t.Errorf("%s didn't read the log as the agent:\n%s", args[0], c)
		}
	}
}

func TestHelperRun(t *testing.T) {
	b := deployed(t, "digest")
	out, err := b.run("", "run", "digest", "send it; now")
	if err != nil || out != "did: send it; now\n" {
		t.Fatalf("run: %v %q", err, out)
	}
	// As the agent's own user, so its run log stays its own, and with
	// every setting the unit sandboxes and limits the agent with.
	c := b.calls()
	_, sandbox, _ := strings.Cut(unitFile, "DynamicUser=yes")
	sandbox, _, _ = strings.Cut(sandbox, "[Install]")
	for line := range strings.Lines("DynamicUser=yes" + sandbox) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if want := "-p " + strings.ReplaceAll(line, "%i", "digest"); !strings.Contains(c, want) {
			t.Errorf("run without %s:\n%s", want, c)
		}
	}
	if out, err := b.run("", "run", "digest", ""); err == nil {
		t.Errorf("an empty task ran: %q", out)
	}
}

func TestHelperSaysWhenItsOlder(t *testing.T) {
	b := newFakeBox(t)
	if out, _ := b.run("", "frobnicate", "digest"); !strings.Contains(out, "tomte-helper: unknown command") {
		t.Errorf("out = %q", out)
	}
}

func TestHelperRollback(t *testing.T) {
	b := deployed(t, "digest")
	if out, err := b.run("", "rollback", "digest"); err == nil || !strings.Contains(out, "no previous binary") {
		t.Errorf("rollback with nothing to go back to: %v %q", err, out)
	}
	b.run(agent("digest")+"# v2\n", "stage", "def")
	b.run("", "activate", "def", "digest")

	d := filepath.Join(b.root, "digest")
	read := func(f string) string { c, _ := os.ReadFile(filepath.Join(d, f)); return string(c) }
	out, err := b.run("", "rollback", "digest")
	if err != nil || !strings.Contains(out, `"name":"digest"`) {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if read("digest") != agent("digest") || !strings.HasSuffix(read("digest.prev"), "# v2\n") {
		t.Errorf("after a rollback: %q, prev %q", read("digest"), read("digest.prev"))
	}
	// A second rollback undoes the first.
	b.run("", "rollback", "digest")
	if !strings.HasSuffix(read("digest"), "# v2\n") || read("digest.prev") != agent("digest") {
		t.Errorf("after two rollbacks: %q, prev %q", read("digest"), read("digest.prev"))
	}
	if _, err := os.Stat(filepath.Join(d, "digest.next")); err == nil {
		t.Error("digest.next was left behind")
	}
}

func TestHelperRemove(t *testing.T) {
	for _, purge := range []bool{false, true} {
		b := deployed(t, "digest")
		b.write(filepath.Join(b.private, "digest", "digest.jsonl"), "{}\n", 0o600)
		args := []string{"rm", "digest"}
		if purge {
			args = append(args, "purge")
		}
		if out, err := b.run("", args...); err != nil {
			t.Fatalf("rm: %v\n%s", err, out)
		}
		if !strings.Contains(b.calls(), "systemctl disable --now --quiet tomte@digest") {
			t.Errorf("calls:\n%s", b.calls())
		}
		if _, err := os.Stat(filepath.Join(b.root, "digest")); err == nil {
			t.Error("/srv/tomte/digest is still there")
		}
		if _, err := os.Stat(filepath.Join(b.private, "digest")); (err == nil) == purge {
			t.Errorf("purge %v: run log there: %v", purge, err == nil)
		}
		if _, err := os.Stat(b.root); err != nil {
			t.Errorf("the root went too: %v", err)
		}
		if out, err := b.run("", "rm", "digest"); err == nil || !strings.Contains(out, "no agent named digest") {
			t.Errorf("rm twice: %v %q", err, out)
		}
	}
}

// journalAgent is agent, built with tomte.ReadsJournal.
func journalAgent(name string) string {
	return strings.Replace(agent(name), `"schedules"`, `"journal":true,"schedules"`, 1)
}

func TestHelperLetsAnAgentReadTheJournal(t *testing.T) {
	b := deployed(t, "watch")
	dropin := filepath.Join(b.dir, "units", "tomte@watch.service.d", "journal.conf")
	if _, err := os.Stat(dropin); err == nil {
		t.Fatal("an agent that didn't ask for the journal can read it")
	}

	b.run(journalAgent("watch"), "stage", "def")
	if out, err := b.run("", "activate", "def", "watch"); err != nil {
		t.Fatalf("activate: %v\n%s", err, out)
	}
	if c, err := os.ReadFile(dropin); err != nil || string(c) != "[Service]\nSupplementaryGroups=systemd-journal\n" {
		t.Fatalf("drop-in: %q %v", c, err)
	}
	if c := b.calls(); !strings.Contains(c, "systemctl daemon-reload") {
		t.Errorf("calls:\n%s", c)
	}
	b.run("", "run", "watch", "logins")
	if c := b.calls(); !strings.Contains(c, "SupplementaryGroups=systemd-journal") {
		t.Errorf("a one-off run without the group:\n%s", c)
	}

	// Going back to a binary that didn't ask takes the group away.
	if out, err := b.run("", "rollback", "watch"); err != nil {
		t.Fatalf("rollback: %v\n%s", err, out)
	}
	if _, err := os.Stat(dropin); err == nil {
		t.Error("the drop-in outlived a rollback to a binary without the journal")
	}
	b.run("", "rollback", "watch")
	if out, err := b.run("", "rm", "watch"); err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Dir(dropin)); err == nil {
		t.Error("rm left the drop-in directory")
	}
}
