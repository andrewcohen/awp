package cli

import (
	"sync"
	"time"

	"github.com/andrewcohen/awp/internal/jj"
)

// workspaceHead is what the deck's rows want to know about a workspace's jj
// state: which change it is on, what that change says, and where the remote
// bookmark it tracks was last seen.
type workspaceHead struct {
	changeID         string
	bookmarkCommitID string
	desc             string
}

// headSpec names one workspace to read: its path, and the bookmark whose
// remote tip belongs on the row (empty for a workspace with none).
type headSpec struct {
	path, bookmark string
}

// headEnricher reads workspaceHeads, and remembers them.
//
// Two `jj log` subprocesses per workspace is nothing once. The deck does it for
// every workspace in the global state file — 51 of them on the machine this was
// written on, across ten repos, whether or not the row is on screen — every
// refreshInterval, forever. Measured, that fan-out is ~3 s of CPU per refresh
// and the refresh is every 5 s: a deck sitting still with nobody typing at it
// spent most of a core doing it, which is what "baseline CPU" turned out to
// mean.
//
// The saving is not a smaller fan-out but a cheaper question. What those
// commands read only changes when the repo records an operation, so
// jj.OpHead answers "would this say anything new" from the filesystem, and an
// unchanged answer is served from the map. At idle — which is the case this
// exists for, since it is the one that runs unattended for hours — nothing has
// recorded an operation and no subprocess runs at all.
//
// A path that is not a jj repo at all, which is what the state file's entries
// for deleted workspaces look like, is answered without a subprocess too: there
// is nothing to read, and forking to be told so is what those entries used to
// cost.
//
// Held by the caller rather than package-scoped so a second deck-shaped reader
// in the same process — `awp workspace attention` builds its rows the same way
// — gets its own, and tests get a cold one.
type headEnricher struct {
	mu    sync.Mutex
	cache map[string]headEntry
}

type headEntry struct {
	// sig is the repo's operation head at the time head was read, plus the
	// bookmark that was read against it. Empty means "could not tell", which
	// never matches and so always re-reads.
	sig  string
	head workspaceHead
}

func newHeadEnricher() *headEnricher {
	return &headEnricher{cache: map[string]headEntry{}}
}

// heads reads every spec, concurrently, and returns what it learned keyed by
// path. Specs it could not finish in time are absent — see the timeout note at
// the call site — and stay absent from the cache too, so the next refresh
// retries them.
func (h *headEnricher) heads(j *jj.Client, specs []headSpec, timeout time.Duration) map[string]workspaceHead {
	if j == nil || len(specs) == 0 {
		return nil
	}
	live := make(map[string]workspaceHead, len(specs))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, s := range specs {
		wg.Add(1)
		go func(s headSpec) {
			defer wg.Done()
			head, ok := h.head(j, s)
			if !ok {
				return
			}
			mu.Lock()
			live[s.path] = head
			mu.Unlock()
		}(s)
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
	}
	mu.Lock()
	defer mu.Unlock()
	out := make(map[string]workspaceHead, len(live))
	for k, v := range live {
		out[k] = v
	}
	return out
}

// head answers one spec, from the cache when the repo has not moved since the
// last read. ok is false only when there is nothing to say and nothing to
// remember — a path with no jj repo under it — which the caller leaves out of
// the result the same way a failed read always was.
func (h *headEnricher) head(j *jj.Client, s headSpec) (workspaceHead, bool) {
	sig, err := jj.OpHead(s.path)
	switch {
	case err != nil:
		// The repo is there but its head would not read. Fall through to the
		// commands, which are the authority anyway, and cache nothing.
		sig = ""
	case sig == "":
		// Not a jj repo — a workspace deleted from disk but still in the state
		// file. jj would fail here; it costs two subprocesses to find that out
		// and the answer is the same every time.
		return workspaceHead{}, false
	default:
		sig += "\x00" + s.bookmark
	}

	if sig != "" {
		h.mu.Lock()
		hit, ok := h.cache[s.path]
		h.mu.Unlock()
		if ok && hit.sig == sig {
			return hit.head, true
		}
	}

	id, desc, _ := j.HeadDescription(s.path)
	var bookmarkCommit string
	if s.bookmark != "" {
		bookmarkCommit, _ = j.BookmarkCommitID(s.path, s.bookmark)
	}
	head := workspaceHead{changeID: id, bookmarkCommitID: bookmarkCommit, desc: desc}
	if sig != "" {
		h.mu.Lock()
		h.cache[s.path] = headEntry{sig: sig, head: head}
		h.mu.Unlock()
	}
	return head, true
}
