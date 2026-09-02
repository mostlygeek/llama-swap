# Session logs

Every working session writes a log here so the project stays auditable,
reproducible, and rollback-friendly.

## Convention

One file per session: `logs/YYYY-MM-DD-<slug>.md`

- `YYYY-MM-DD` — the session date.
- `<slug>` — a short kebab-case description of the change (e.g.
  `fix-login-redirect`, `add-rescan-command`).

Existing entries are the template — match their structure:

1. **Symptom / goal** — what prompted the session.
2. **Diagnosis** — what was investigated and found.
3. **Change** — what was done, with the diff or key edits.
4. **Commands** — exact commands run and their output/result.
5. **Verification** — how the change was confirmed (tests, scanners, manual).
6. **Notes** — reasoning, design choices, errors hit, and follow-ups.

## What to record

Document all activities: connections made, commands run, configuration changes,
files transferred, troubleshooting steps, observations, the reasoning behind
each action, errors encountered and how they were resolved, and the logical
choices made about the design. If it was done, it should be written down.
