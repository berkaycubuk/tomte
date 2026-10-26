# Security

`tomte setup` installs a script that runs as root and a sudoers rule that
lets your ssh user run it, so a hole in Tomte can be a hole in your
machine. Please report one privately, through
[GitHub's private vulnerability reporting](https://github.com/berkaycubuk/tomte/security/advisories/new),
not in a public issue.

Say what you found, how to reproduce it, and what it lets someone do.
You'll hear back within a week. Fixes go into the latest release only.

## What Tomte means to keep

- The ssh user can do no more as root than `tomte-helper` allows: deploy,
  configure, run and remove agents, and read their logs.
- An agent runs as its own user, writes only its state directory, and
  can't read another agent's state directory or env file.
- A Telegram bot takes commands only from its own chat, unless it's made
  `Public()`.

Something that breaks one of these is a vulnerability. What an agent's
own tools and commands do with what they're given is up to the agent's
author.
