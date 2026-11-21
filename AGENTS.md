# AGENTS.md

Conventions for agents working in this repo.

## Commit messages

Use Conventional Commits and Simple English (the `simple-english` skill, Plain mode).

- Format: `type: subject`, or `type(scope): subject`.
- Types: `feat`, `fix`, `docs`, `refactor`, `test`, `build`, `ci`, `chore`.
- Subject: imperative mood, 50 characters or fewer when it fits.
- Body (optional): one fact per line.
- No em-dashes, semicolons, or contractions in the message.
- When a change belongs to an earlier commit, use `git commit --fixup=<sha>`, then rebase with `--autosquash`.

## Prose

All docs, tickets, and chat follow the `simple-english` skill (Plain mode): short sentences, active voice, simple tenses, one word per meaning.

## Tickets

Tickets are markdown files under `.tickets/`. Manage them only with the `tickets-as-code` skill CLI (`ticket_ops.py`). Do not edit frontmatter by hand. Keep published ticket bodies in sync with their GitHub issues (`ticket_ops sync`).

## Skills

Third-party skills are installed with `npx skills add` (project scope, universal agents) and pinned in `skills-lock.json` with commit and content hash. The skill folders under `.agents/skills/` are gitignored; only the lockfile is tracked. Add and update skills through `npx skills add` / `npx skills update`, never by copying folders or tracking them.

An adversarial security review (2026-11-21, all 18 pinned skills) found no malicious content, but these nuances apply when the skills are in use:

- `golang-troubleshooting`: `references/methodology.md` tells the agent to run `env | grep DATABASE` and `env | grep API_KEY`. These print secret values into the transcript. Never run them as written. Check presence with names only: `env | grep -E 'API_KEY|DATABASE' | cut -d= -f1`.
- `golang-continuous-integration`: the `assets/claude-code-review.yml` workflow installs all skills from unpinned HEAD on every PR in a job that holds `ANTHROPIC_API_KEY`. Do not adopt that asset until the install is pinned to a tag or commit. Its `assets/renovate.json` auto-merges major GitHub Actions updates. Add `"matchUpdateTypes": ["minor", "patch"]` to the `github-actions` rule before use.
- `docker-build-strategies`: it delegates to three companion skills that are not installed (`docker-project-foundations`, `docker-compose-patterns`, `docker-destructive-guardrails`). The destructive-operation guardrails it references do not exist locally. Confirm with the user before any destructive docker operation (prune, volume removal, image deletion).
- `k8s-*` skills: destructive commands are fenced by prose only ("Proposed fix" blocks with undo lines), not by tool restrictions. Never run a "Proposed fix" command yourself. Hand it to the user. Do not read cluster Secrets. An "Ask first" command runs only after the user approves that exact command.
- `golang-*` skills: some tool installs use `@latest`. Prefer pinned versions, or `go get -tool`, which pins the version in `go.mod`.

## Checks

- Install the hook once per clone: `uv run pre-commit install`. It
  hard-blocks a commit when any check fails.
- Run `pre-commit run --all-files` before you commit. Check its exit code.
  Do not pipe its output through `tail` or another command. A pipe hides
  the exit code and lets a failure pass.
- CI mirrors the checks in `.github/workflows/verify.yml`.
