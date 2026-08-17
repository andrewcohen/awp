package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/andrewcohen/awp/internal/config"
	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/workspace"
)

// intentTimeout bounds the model call behind the free-text box.
//
// Generous for a one-shot prompt that answers in a couple of seconds, and
// short enough that a wedged or offline agent does not hold a person at a
// disabled text box. Running out is not a failure the user has to handle —
// it lands them in the structured form, so the only cost of the wait is the
// wait.
const intentTimeout = 30 * time.Second

// intentPromptFlag runs the agent headlessly: one prompt in, one answer
// out, no terminal. Claude-specific, which is why headlessIntentArgv gates
// on the configured agent being Claude.
const intentPromptFlag = "-p"

// headlessIntentArgv is the command that resolves free text, and whether
// there is one at all.
//
// Built from the same config.AgentInvocation the captain's argv is built
// from (see captainAgentArgv), so a user who has pointed awp at a
// particular Claude build or model gets that one here too rather than a
// second, differently-configured agent appearing behind a different door.
//
// ok is false for a non-Claude agent, on the same reasoning as
// captainPreambleFile: `-p` is Claude's spelling of headless, and guessing
// at another agent's would produce a confusing failure — a binary that
// exists, runs, and does the wrong thing — where declining produces the
// structured form. The gate is the agent *name*, so a wrapper script named
// something else opts out; that is the conservative direction.
// Takes the invocation string rather than a repo root so the gate is a
// decision about a string and can be checked as one.
func headlessIntentArgv(invocation string) ([]string, bool) {
	argv := strings.Fields(invocation)
	if len(argv) == 0 {
		return nil, false
	}
	if !strings.Contains(argv[0], "claude") {
		return nil, false
	}
	return argv, true
}

// intentPrompt asks for the four fields, given the projects that exist.
//
// The project list is closed on purpose: the model picks from it or says
// nothing, and resolveWorkspaceIntent discards anything else. A model free
// to name a project invents plausible ones — "backend", "infra" — and the
// result would be a workspace created in a directory nobody chose.
func intentPrompt(text string, projects []deckui.ProjectItem, defaultProject string) string {
	var b strings.Builder

	b.WriteString("Turn a developer's description of what they want to work on ")
	b.WriteString("into the arguments for creating a workspace for it.\n\n")

	b.WriteString("What they wrote:\n")
	b.WriteString(text)
	b.WriteString("\n\n")

	b.WriteString("Answer with a JSON object and nothing else — no prose, no code fence:\n")
	b.WriteString(`{"name": "...", "label": "...", "prompt": "...", "project": "..."}`)
	b.WriteString("\n\n")

	b.WriteString("  name     A directory name: lowercase, hyphen-separated, letters digits and\n")
	b.WriteString("           hyphens only, a handful of words. It has to work as a path.\n")
	b.WriteString("  label    The same thing as a short human-readable phrase. This is what the\n")
	b.WriteString("           deck shows, so it may have spaces and capitals.\n")
	b.WriteString("  prompt   What to tell the coding agent that will do the work. Write it as an\n")
	b.WriteString("           instruction to that agent. Keep the developer's meaning and their\n")
	b.WriteString("           specifics — a PR number, a file, a symbol they named. Do not invent\n")
	b.WriteString("           requirements they did not state.\n")

	if len(projects) > 0 {
		b.WriteString("  project  Which project this belongs to. Choose one of these names exactly:\n")
		for _, p := range projects {
			fmt.Fprintf(&b, "             %s\n", p.Name)
		}
		fmt.Fprintf(&b, "           If they did not say and it is not obvious, answer %q.\n", defaultProject)
	} else {
		fmt.Fprintf(&b, "  project  Answer %q.\n", defaultProject)
	}

	return b.String()
}

// intentReply is the model's answer, before validation.
type intentReply struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Prompt  string `json:"prompt"`
	Project string `json:"project"`
}

// parseIntentReply pulls the JSON object out of an agent's output.
//
// Scans for the outermost braces rather than unmarshalling the whole thing,
// because "answer with JSON and nothing else" is a request and not a
// guarantee: agents wrap answers in ```json fences and preface them with a
// sentence. Being lenient here is the difference between a working box and
// one that drops to the structured form because of a code fence.
func parseIntentReply(out string) (intentReply, error) {
	start := strings.Index(out, "{")
	end := strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return intentReply{}, fmt.Errorf("no JSON object in the agent's answer:\n%s", snippet(out))
	}
	var reply intentReply
	if err := json.Unmarshal([]byte(out[start:end+1]), &reply); err != nil {
		return intentReply{}, fmt.Errorf("the agent's answer is not the expected JSON: %w\n%s", err, snippet(out))
	}
	return reply, nil
}

// snippet bounds output being quoted back into an error message.
func snippet(out string) string {
	s := strings.TrimSpace(out)
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}

// resolveWorkspaceIntent runs the model call and validates its answer.
//
// Every field is checked against something local: the name is re-slugged
// rather than trusted to be a usable directory, the project has to be one
// that exists, and a missing label or prompt falls back to the text the
// user typed. The model is choosing between real options, not authoring
// the arguments outright — which is what makes it safe to show the result
// and let the user press enter.
// argv is the agent command to ask, from headlessIntentArgv. Passed in
// rather than looked up here so that what this function does is decide,
// given an answer — the configuration question is the caller's, and a test
// can exercise the deciding without a config file on disk.
func resolveWorkspaceIntent(ctx context.Context, runner Runner, argv []string, text, defaultRepoRoot string, projects []deckui.ProjectItem) (deckui.WorkspaceIntent, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return deckui.WorkspaceIntent{}, errors.New("new workspace: nothing typed — describe what you want to work on")
	}
	if strings.TrimSpace(defaultRepoRoot) == "" {
		return deckui.WorkspaceIntent{}, errors.New("new workspace: no project to fall back on — select a row in a project first")
	}

	// The fallback is built first so it is what every early return carries.
	// A caller that gets an error still gets a usable intent alongside it,
	// which is what lets the failure path open a pre-filled form instead of
	// an empty one.
	fallback := fallbackIntent(text, defaultRepoRoot)
	if len(argv) == 0 {
		return fallback, errors.New("new workspace: no agent to resolve free text with — fill the form in instead")
	}

	ctx, cancel := context.WithTimeout(ctx, intentTimeout)
	defer cancel()

	args := make([]string, 0, len(argv)+1)
	args = append(args, argv[1:]...)
	args = append(args, intentPromptFlag, intentPrompt(text, projects, fallback.Project))
	out, err := runner.Run(ctx, defaultRepoRoot, argv[0], args...)
	if err != nil {
		if ctx.Err() != nil {
			return fallback, fmt.Errorf("new workspace: %s did not answer within %s — fill the form in instead", argv[0], intentTimeout)
		}
		return fallback, fmt.Errorf("new workspace: could not ask %s what to create: %w", argv[0], err)
	}

	reply, err := parseIntentReply(out)
	if err != nil {
		return fallback, fmt.Errorf("new workspace: %w", err)
	}

	intent := fallback
	if name := workspace.SlugFromText(reply.Name); strings.TrimSpace(reply.Name) != "" {
		intent.Name = name
	}
	if label := strings.TrimSpace(reply.Label); label != "" {
		intent.Label = label
	}
	if prompt := strings.TrimSpace(reply.Prompt); prompt != "" {
		intent.Prompt = prompt
	}
	if p, ok := matchProject(reply.Project, projects); ok {
		intent.Project = p.Name
		intent.RepoRoot = p.Path
	}
	return intent, nil
}

// fallbackIntent is what free text resolves to with no model involved: the
// sentence itself as the label and the prompt, a locally-derived slug for
// the name, and the row's own project.
//
// This is the offline answer, and it is deliberately a complete one. Every
// failure in this file returns it, so the path when the agent is missing,
// slow, or wrong is a structured form already filled in — the user edits
// four fields rather than starting over.
func fallbackIntent(text, defaultRepoRoot string) deckui.WorkspaceIntent {
	text = strings.TrimSpace(text)
	return deckui.WorkspaceIntent{
		Name:     workspace.SlugFromText(text),
		Label:    text,
		Prompt:   text,
		Project:  projectNameFor(defaultRepoRoot),
		RepoRoot: defaultRepoRoot,
	}
}

// intentResolverFromRoots is the deck's resolver: the model call above,
// wrapped as a tea.Cmd, over the projects discovered under roots.
//
// Projects are discovered per call rather than captured once. It is a walk
// of a few directories against a call that takes seconds, and a project
// cloned since the deck started is one a person may well be typing about.
//
// The command never fails to produce a message: an error travels in
// IntentDoneMsg.Err alongside the fallback intent, because the deck's
// answer to a failed resolution is a pre-filled form rather than a dead
// box.
func intentResolverFromRoots(runner Runner, roots []string, maxDepth int) deckui.IntentResolver {
	return func(text string, defaultRepoRoot string) tea.Cmd {
		return func() tea.Msg {
			invocation := config.AgentInvocation(defaultRepoRoot)
			argv, ok := headlessIntentArgv(invocation)
			if !ok {
				return deckui.IntentDoneMsg{
					Text:   text,
					Intent: fallbackIntent(text, defaultRepoRoot),
					Err:    fmt.Errorf("new workspace: %q is not a Claude agent, so free text cannot be resolved — fill the form in instead", invocation),
				}
			}
			projects, err := discoverProjects(roots, maxDepth)
			if err != nil {
				// Not fatal: with no list the model is told to answer with
				// the row's own project, which is the default anyway.
				projects = nil
			}
			intent, err := resolveWorkspaceIntent(context.Background(), runner, argv, text, defaultRepoRoot, projects)
			return deckui.IntentDoneMsg{Text: text, Intent: intent, Err: err}
		}
	}
}

// matchProject resolves a name the model returned to a project that exists.
//
// Case-insensitive on the name only — never a path, and never a prefix
// match. An unrecognized answer is not an error the user has to resolve; it
// simply does not move the workspace off the row's own project, which is
// where it was going to be created anyway.
func matchProject(name string, projects []deckui.ProjectItem) (deckui.ProjectItem, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return deckui.ProjectItem{}, false
	}
	for _, p := range projects {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return deckui.ProjectItem{}, false
}
