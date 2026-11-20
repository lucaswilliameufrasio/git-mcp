# Changelog

Mudanças do git-mcp. As entradas saem do histórico Git e são revisadas no
pull request de release antes da tag.

## [0.3.1] - 2026-11-20


### CI / Release

- **release:** List what shipped and publish installers


The release page was only a static header, so the tag did not say what
changed. GoReleaser now groups the commits since the previous tag,
publishes one SHA-256 per file, and attaches the installers. Prepare
Release starts CI because the bot token does not.


## [0.3.0] - 2026-11-11


### Features

- **console:** Add call details, filters and paging to the audit


Each audit event now also records the mode, the repository path, the
branch, tag and remote names the call named, the result message and up
to five block reasons. The confirmation string and oids stay out, and
every text is clipped to 300 characters.

The audit page searches every retained call instead of the latest 200,
50 per page, filtered by tool, status, period and free text through a
GET form, so a search is an address. The console's form-action policy
goes from 'none' to 'self' for that form.


## [0.2.0] - 2026-11-11


### CI / Release

- **release:** Keep merge commits out of the changelog



### Features

- Add local metrics console and cli commands


Each stdio process now records one metadata file per finished tool call
(tool, start, duration, status) under the user's state directory. No
repository path, branch, oid or argument is stored, and files expire
after 30 days.

`git-mcp console` serves read-only overview, monitoring and audit pages
on IPv4 loopback from those files. Its look is recorded in
docs/design-system.md, taken from the jira-mcp console.

New commands: doctor, tools, stats, version and help. An unknown command
line now exits with the usage page and code 2 instead of starting the
server.


## [0.1.0] - 2026-11-10


### Features

- Add local git workflow mcp server


Pin Go 1.27.4 with mise, upgrade golang.org/x/sys past the Windows
NewNTUnicodeString overflow, and add the release preparation flow.


