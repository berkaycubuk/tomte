# Open-source checklist

Issues found reviewing Tomte before making the repo public, worked through
one by one. Delete this file before the release.

## Must fix

- [x] 1. tomte-helper reads run logs as root, so an agent can symlink its log to any file (`runs`, `ls`)
- [x] 2. `go.mod` asks for Go 1.26; 1.25 is enough
- [x] 3. No SECURITY.md

## Should fix

- [x] 4. No time limit on a run: a hung tool or job blocks its schedule forever
- [x] 5. The run log is read from the start on every run with a daily limit
- [x] 6. tomte-helper's `describe` has no timeout or memory limit
- [x] 7. systemd hardening for tomte@.service and `tomte-helper run`
- [x] 8. The README doesn't say what an agent and the ssh user are trusted with

## Packaging

- [x] 9. Doc comments on `Agent`, `Option`, `Channel` and the channels' `Channel` and `New`
- [x] 10. Runnable examples for pkg.go.dev (`example_test.go`)
- [ ] 11. Tag v0.1.0 and say the API may change before v1 — README says it; the tag is yours to push after the fixes are committed
- [x] 12. Say that telekinesis is made by the same author
- [x] 13. CI: staticcheck, govulncheck, shellcheck
- [x] 14. README: why Tomte, badges, requirements, status
- [x] 15. CONTRIBUTING.md

## Minor

- [x] 16. `MaxSteps` of 0 or less isn't rejected
- [x] 17. Two jobs with the same name aren't rejected
- [x] 18. `truncate` and `snippet` can cut a character in half
- [x] 19. `ls`'s TODAY compares the agent's time zone with the box's date

## Found while working through the list

- [ ] 20. The local Go (1.26.4) has 15 standard library vulnerabilities that agents reach (crypto/tls, net/http…), and `tomte deploy` builds with it — mise.toml now asks for the latest 1.26; run `mise install`, then redeploy the agents
