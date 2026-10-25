// SSH watch tells you on Telegram when someone logs in to this machine
// over ssh as a user, from an address or with a key it hasn't seen
// before. It's one job and no model: noticing something new needs no
// judgment.
//
//	TELEGRAM_TOKEN=… TELEGRAM_CHAT=… go run ./examples/ssh-watch run logins
//
// ROAMING_USERS lists users who log in from anywhere, like a CI system
// deploying from whichever runner it got (deploy,ci): for them only a new
// key is news, not a new address. ROAMING_KEYS does the same for keys,
// by fingerprint (SHA256:…), for when CI logs in as a user you also log in
// as yourself.
//
// Its first run learns the logins of the last 30 days and lists them, so
// you can check them once; after that it's quiet until something's new.
// It reads sshd's lines from the system journal, which needs the
// systemd-journal group; tomte deploy gives it that, and only that,
// because of tomte.ReadsJournal.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/berkaycubuk/tomte"
	"github.com/berkaycubuk/tomte/channels/telegram"
)

func main() {
	opts := []tomte.Option{
		tomte.Job("@every 1m", "logins", logins),
		tomte.ReadsJournal(),
	}
	if token := os.Getenv("TELEGRAM_TOKEN"); token != "" {
		opts = append(opts, tomte.SendTo(telegram.New(token, os.Getenv("TELEGRAM_CHAT"))))
	}
	tomte.New("ssh-watch", opts...).Run()
}

// known is what the watch remembers between runs, in its state directory.
type known struct {
	Last time.Time            `json:"last"` // when the last run started
	Seen map[string]time.Time `json:"seen"` // "user berkay", "from 1.2.3.4", "key ED25519 SHA256:…": first seen
}

type login struct {
	at                      time.Time
	user, from, method, key string // key is "" for a password
}

// sshd logs: Accepted publickey for berkay from 1.2.3.4 port 50022 ssh2: ED25519 SHA256:…
var accepted = regexp.MustCompile(`^Accepted (\S+) for (\S+) from (\S+) port \d+ ssh2(?:: (\S+ \S+))?`)

func logins(ctx context.Context) (string, error) {
	path := filepath.Join(tomte.StateDir(), "known.json")
	k, err := load(path)
	if err != nil {
		return "", err
	}
	first := k.Last.IsZero()
	found, err := read(ctx, k)
	if err != nil {
		return "", err
	}

	roamingUsers := list(os.Getenv("ROAMING_USERS"))
	roamingKeys := list(os.Getenv("ROAMING_KEYS"))
	var news []string
	for _, l := range found {
		var why []string
		for _, f := range []struct{ fact, says string }{
			{"user " + l.user, "new user"},
			{"from " + l.from, "new address"},
			{"key " + cmp(l.key, l.method), "new key"},
		} {
			if f.says == "new address" && (slices.Contains(roamingUsers, l.user) || slices.Contains(roamingKeys, fingerprint(l.key))) {
				continue
			}
			if _, ok := k.Seen[f.fact]; !ok {
				k.Seen[f.fact] = l.at
				why = append(why, f.says)
			}
		}
		if len(why) > 0 {
			news = append(news, fmt.Sprintf("- %s: %s (%s)", l.at.Local().Format("Mon 2 Jan 15:04"), l, strings.Join(why, ", ")))
		}
	}
	if err := save(path, k); err != nil {
		return "", err
	}

	host, _ := os.Hostname()
	switch {
	case first:
		return learned(host, found), nil
	case len(news) == 0:
		return "NOTHING", nil
	}
	return fmt.Sprintf("**New ssh login on %s**\n%s\n\nIf that wasn't you, someone else has a way in.", host, strings.Join(news, "\n")), nil
}

func (l login) String() string {
	s := fmt.Sprintf("`%s` from `%s`", l.user, l.from)
	if l.key != "" {
		return s + " with `" + l.key + "`"
	}
	return s + " with a " + l.method
}

// fingerprint is SHA256:… from "ED25519 SHA256:…".
func fingerprint(key string) string {
	if _, fp, ok := strings.Cut(key, " "); ok {
		return fp
	}
	return key
}

// list splits a comma-separated setting, leaving out empty entries: an
// unset ROAMING_KEYS would otherwise hold "", which is what a password
// login's key is, and passwords would roam.
func list(s string) []string {
	var l []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			l = append(l, f)
		}
	}
	return l
}

func cmp(s, or string) string {
	if s != "" {
		return s
	}
	return or
}

// learned lists the logins the first run found, each way in once.
func learned(host string, found []login) string {
	if len(found) == 0 {
		return fmt.Sprintf("Watching ssh logins on %s. None in the last 30 days.", host)
	}
	type way struct{ user, from, how string }
	count := map[way]int{}
	last := map[way]time.Time{}
	var ways []way
	for _, l := range found {
		w := way{l.user, l.from, cmp(l.key, l.method)}
		if count[w] == 0 {
			ways = append(ways, w)
		}
		count[w]++
		last[w] = l.at
	}
	lines := []string{fmt.Sprintf("Watching ssh logins on %s. These are the ways in from the last 30 days; from now on you'll hear only of new ones.", host), ""}
	for _, w := range ways {
		lines = append(lines, fmt.Sprintf("- `%s` from `%s` with `%s`: %d, last %s", w.user, w.from, w.how, count[w], last[w].Local().Format("2 Jan")))
	}
	return strings.Join(lines, "\n")
}

// read returns the logins logged since the last run, or in the last 30
// days on the first. Runs overlap by a minute, so a line logged late
// isn't missed; logins read twice are known the second time.
func read(ctx context.Context, k *known) ([]login, error) {
	if err := canReadJournal(); err != nil {
		return nil, err
	}
	since := "-30d"
	if !k.Last.IsZero() {
		since = k.Last.Add(-time.Minute).UTC().Format(time.DateTime) + " UTC"
	}
	k.Last = time.Now()

	// A machine on the internet logs thousands of break-in attempts a day;
	// journalctl keeps only the logins, and its output is read as it comes.
	cmd := exec.CommandContext(ctx, "journalctl", "-t", "sshd", "-t", "sshd-session", "--grep", "^Accepted ",
		"--since", since, "-o", "json", "--output-fields", "MESSAGE", "--no-pager")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	var found []login
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e struct {
			Message any    `json:"MESSAGE"`              // text, or bytes as numbers when it isn't UTF-8
			Time    string `json:"__REALTIME_TIMESTAMP"` // microseconds
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		msg, _ := e.Message.(string)
		if m := accepted.FindStringSubmatch(msg); m != nil {
			us, _ := strconv.ParseInt(e.Time, 10, 64)
			found = append(found, login{at: time.UnixMicro(us), method: m[1], user: m[2], from: m[3], key: m[4]})
		}
	}
	if err := cmd.Wait(); err != nil {
		// journalctl --grep exits 1 when nothing matches.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 || stderr.Len() > 0 {
			return nil, fmt.Errorf("journalctl: %v: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	return found, sc.Err()
}

// canReadJournal says why not, if the journal would show this user only
// its own lines: sshd's logins are root's, and missing them would be quiet.
func canReadJournal() error {
	if os.Geteuid() == 0 {
		return nil
	}
	groups, _ := os.Getgroups()
	for _, name := range []string{"systemd-journal", "adm", "wheel"} {
		if g, err := user.LookupGroup(name); err == nil {
			if gid, _ := strconv.Atoi(g.Gid); slices.Contains(groups, gid) {
				return nil
			}
		}
	}
	return errors.New("can't read the system journal: not root, nor in the systemd-journal group; is the box's tomte-helper older than tomte.ReadsJournal? Run `tomte setup` again, then deploy")
}

func load(path string) (*known, error) {
	k := &known{Seen: map[string]time.Time{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return k, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, k); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if k.Seen == nil {
		k.Seen = map[string]time.Time{}
	}
	return k, nil
}

// save replaces the file whole, so a run stopped halfway can't leave half.
func save(path string, k *known) error {
	b, err := json.MarshalIndent(k, "", "\t")
	if err != nil {
		return err
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
