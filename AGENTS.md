# AGENTS.md

Guidance for AI coding agents working on **awp** (Agentic Workspace Pilot), a Go CLI and TUI application.

## Purpose

This repository is under active redesign. Prior behavior should not be treated as a requirement.
Focus on the current task request and keep solutions simple, correct, and easy to change.

## Working Principles

- Prefer small, incremental changes.
- Clarify assumptions before implementing anything ambiguous.
- Favor readability over cleverness.
- Keep dependencies minimal unless explicitly requested.
- Preserve backward compatibility only when the task asks for it.

## Engineering Standards

- Keep code idiomatic for the language and project style.
- Add or update tests for behavior you change.
- Keep public interfaces minimal and stable.
- Handle errors explicitly and return actionable messages.
- Avoid hidden global state where possible.

## Security & Safety

- Treat all external input as untrusted.
- Avoid command injection, path traversal, and unsafe shell usage.
- Use least-privilege defaults for files, network calls, and credentials.
- Never hardcode secrets or tokens.

## Validation Before Handoff

When applicable, run:

- `go test ./...`
- `go vet ./...`
- `go build ./...`

If you cannot run something, state what was not run and why.

## Version Control

- Prefer **Jujutsu (`jj`)** workflows by default.
- Use git only when explicitly requested or when `jj` cannot do the task.
- Name new `jj` bookmarks with the `andrew/` prefix.

## Spec Workflow

- Store feature specs under `specs/`.
- Start from `specs/spec-template.md`.
- Create a new spec by copying/renaming the template to `specs/<ID>-<feature>-spec.md`.
- Prefer `scripts/new-spec "<feature name>"` to generate the filename automatically.
- `<ID>` must be monotonic and collision-resistant across contributors.
- Use ID format: `YYYYMMDD-<rand4>` (example: `20260409-7k2m`).
- `YYYYMMDD` provides chronological ordering; `<rand4>` (lowercase letters/digits) reduces collision risk for parallel work.
- Ask clarifying questions until the spec is solid before implementation.
- Ensure each spec includes: user problem, scope/non-goals, UX, implementation steps, acceptance criteria, and QA plan.
- Treat the spec as a primary code-review artifact for humans.
- When implementation deviates from the spec, update the spec in the same change (decisions, scope, acceptance criteria, QA notes) so it stays accurate.

## Communication

- Summarize what changed, where, and why.
- Call out tradeoffs and follow-up work clearly.
- Be concise and concrete.
