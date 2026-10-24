# Sets a machine up for tomte agents, as root; `tomte setup` runs it over
# ssh. Running it again upgrades what it installs and changes the limits;
# agents and their env files are left alone.
set -eu
umask 022

command -v systemctl >/dev/null || { echo "tomte needs systemd, and this machine has no systemctl" >&2; exit 1; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

cat >"$tmp/unit" <<'TOMTE_EOF'
{{.Unit}}TOMTE_EOF
cat >"$tmp/slice" <<'TOMTE_EOF'
{{.Slice}}TOMTE_EOF
cat >"$tmp/helper" <<'TOMTE_EOF'
{{.Helper}}TOMTE_EOF

mkdir -p /srv/tomte /usr/local/libexec
install -m 0644 "$tmp/unit" /etc/systemd/system/tomte@.service
install -m 0644 "$tmp/slice" /etc/systemd/system/tomte.slice
install -m 0755 "$tmp/helper" /usr/local/libexec/tomte-helper
systemctl daemon-reload

# The ssh user may run the helper as root without a password, and nothing
# else. Root needs no rule.
user=${SUDO_USER:-root}
if [ "$user" != root ]; then
	command -v visudo >/dev/null || { echo "sudo isn't installed" >&2; exit 1; }
	echo "$user ALL=(root) NOPASSWD: /usr/local/libexec/tomte-helper" >"$tmp/sudoers"
	visudo -cqf "$tmp/sudoers"
	install -m 0440 "$tmp/sudoers" /etc/sudoers.d/tomte
	if ! sudo -n -l -U "$user" /usr/local/libexec/tomte-helper >/dev/null 2>&1; then
		echo "warning: sudo doesn't read /etc/sudoers.d; add \"@includedir /etc/sudoers.d\" to /etc/sudoers" >&2
	fi
fi

echo "$(hostname) is set up for tomte agents; together they may use {{.Memory}} of memory and {{.CPU}} CPU."
