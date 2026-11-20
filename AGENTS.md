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

## Checks

Run `pre-commit run --all-files` before you commit. CI mirrors it in `.github/workflows/verify.yml`.
