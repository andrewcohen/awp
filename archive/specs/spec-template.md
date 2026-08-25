# Feature Spec Template

## Metadata
- **Spec ID**: `YYYYMMDD-<rand4>`
- **Feature name**:
- **Owner**:
- **Status**: Discovery / Planned / In Progress / Done
- **Last updated**:

## Goal
What user outcome does this feature deliver?

## User Problem
Who is the user and what pain are we solving?

## Scope
### In scope (v1)
-

### Out of scope (v1)
-

## UX
### CLI
-

### TUI
-

## Discovery Questions
1. Who is the first user?
2. When do they use this feature?
3. What exact output/result do they need?
4. What data sources are required?
5. What is the smallest useful slice?
6. What are explicit non-goals?
7. What does “done” look like?

## Spec Change Log
- YYYY-MM-DD: Initial draft.
- YYYY-MM-DD: Decision/scope change during implementation (update decisions, acceptance criteria, and QA plan).

## Implementation Plan
1.
2.
3.

## Acceptance Criteria
- [ ]
- [ ]
- [ ]

## QA / Human Review Test Plan
### Setup
- [ ] Prerequisites installed and available in PATH (e.g., `jj`, `tmux`, project binary).
- [ ] Test environment/state prepared (repo, config, seed data, clean shell).

### Core Happy Path
- [ ] Verify the primary user workflow end-to-end.
- [ ] Confirm expected output/messages and resulting state changes.

### Edge Cases & Failure Modes
- [ ] Missing required input shows actionable errors.
- [ ] Duplicate/conflicting input behavior matches spec decisions.
- [ ] Outside-supported-environment behavior is clear and safe.

### Regression Checks
- [ ] Adjacent commands/workflows still behave correctly.
- [ ] Backward-compatible behavior (if required) remains intact.

### Reviewer Notes
- Capture commands run, observed output, and any follow-up issues.

## Validation
- [ ] `go test ./...`
- [ ] `go vet ./...`
- [ ] `go build ./...`
