Rules:

- releases are tagged commits off the `main` branch
- version numbers are sequential like v1, v2, ... v255
- determine the changes from the HEAD of `main` to the last release tag
- add a new version for the release by following the template example below

Template Example:

```
## v255

A short, reader-focused overview of the most important changes since the last
release. Group related work into two to four themes, covering material new
features, fixes, and behavior changes; leave PR-level detail to the list below.
Keep it to one to three sentences and wrap lines at 80 characters.

- [PR #1](https://github.com/mostlygeek/llama-swap/pull/1) Title from PR: summary of the changes it introduced
- [PR #2](https://github.com/mostlygeek/llama-swap/pull/2) Title from PR: summary of the changes it introduced
- [PR #3](https://github.com/mostlygeek/llama-swap/pull/2) Title from PR: summary of the changes it introduced
```
