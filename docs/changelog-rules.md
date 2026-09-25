Rules:

- releases are tagged commits off the `main` branch
- version numbers are sequential like v1, v2, ... v255
- determine the changes from the HEAD of `main` to the last release tag
- read each commit's full message and its pull request description; use them
  to explain what changed, why, and what caused any fixed problem
- add a new version for the release by following the template example below
- for each landed commit, credit the contributor with a link to their GitHub
  profile, e.g. `by [@username](https://github.com/username)`
- do not add attribution for commits by the maintainer (`mostlygeek`)

Template Example:

```
## v255

Tell the story of the release in plain language. Write a short paragraph for
each main theme, using simple sentences that say what changed and why it
helps. When describing a fix, briefly say what caused the problem. Put
anything users must do when upgrading in its own paragraph.

Use ASCII only and wrap lines at 80 characters.

- [PR #1](https://github.com/mostlygeek/llama-swap/pull/1) Title from PR: summary of the changes it introduced by [@contributor](https://github.com/contributor)
- [PR #2](https://github.com/mostlygeek/llama-swap/pull/2) Title from PR: summary of the changes it introduced
- [PR #3](https://github.com/mostlygeek/llama-swap/pull/3) Title from PR: summary of the changes it introduced by [@another-contributor](https://github.com/another-contributor)
```
