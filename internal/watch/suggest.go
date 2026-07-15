package watch

import (
	"fmt"
	"path/filepath"
)

// SuggestConfigPrompt returns a ready-to-paste prompt that asks a coding
// agent to inspect the given repo and write a `dev_loop` block into its
// .awp/config.json. It is what `awp watch` offers when a repo has no dev
// loop configured, rather than silently guessing the wrong gates.
func SuggestConfigPrompt(repoRoot string) string {
	configPath := ".awp/config.json"
	if repoRoot != "" {
		configPath = filepath.Join(repoRoot, ".awp", "config.json")
	}
	return fmt.Sprintf(`Configure the awp dev loop for this repository so `+"`awp watch`"+` can track agent progress.

Inspect the project to discover its real development loop:
- Read README, CLAUDE.md/AGENTS.md, and any Makefile / mise.toml / package.json
  scripts / justfile for the canonical format, lint, test, build, and commit
  commands (the "validation before handoff" gates).
- Identify the natural phases a single unit of work passes through
  (e.g. explore, implement, verify, commit).

Then write (or merge into) %s a "dev_loop" block in this exact shape:

{
  "dev_loop": {
    "phases": ["explore", "implement", "verify", "commit"],
    "gates": [
      { "name": "fmt",    "phase": "verify", "match": "<regex matching the format command>" },
      { "name": "lint",   "phase": "verify", "match": "<regex matching the lint command>" },
      { "name": "test",   "phase": "verify", "match": "<regex matching the test command>" },
      { "name": "build",  "phase": "verify", "match": "<regex matching the build command>" },
      { "name": "commit", "phase": "commit", "match": "<regex matching the commit/push command>" }
    ]
  }
}

Rules:
- "match" is a Go regular expression tested against the shell command the agent
  runs; keep each pattern tight enough to avoid false matches (e.g. "pnpm test"
  or "go test", not just "test").
- Use the phase names that fit this project; every gate's "phase" must be one of
  the "phases" entries.
- Preserve any existing keys in the config file — only add/replace "dev_loop".

Report the final dev_loop block you wrote.`, configPath)
}
