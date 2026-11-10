Rules:

- releases are tagged commits off the `main` branch
- version numbers are sequential like v1, v2, ... v255
- each release entry is its own file named `changelog/v<version>.md`,
  for example `changelog/v255.md`, starting with the H2 heading below
- determine the changes from the HEAD of `main` to the last release tag
- read each commit's full message and its pull request description; use them
  to explain what changed, why, and what caused any fixed problem
- for each landed commit, credit the contributor with a link to their GitHub
  profile, e.g. `by [@username](https://github.com/username)`
- summary for the release by following the template example below
  - a H2 heading with the version number and date
  - a single summary line of each commit in the release
  - a placeholder for `> maintainer notes`

Template Example:

```
## v255 (Jan 2, 2026)

A very short one line summary of the key changes in this release.

> maintainer notes

----

- [PR #1](https://github.com/mostlygeek/llama-swap/pull/1) Title from PR: one line, very short summary of changes.  [@contributor](https://github.com/contributor)
- [PR #2](https://github.com/mostlygeek/llama-swap/pull/1) Title from PR: one line, very short summary of changes.  [@contributor](https://github.com/contributor)
- ...
```
