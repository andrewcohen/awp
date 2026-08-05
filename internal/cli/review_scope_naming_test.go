package cli

import (
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/github"
	"github.com/andrewcohen/awp/internal/review"
)

// Every surface that names what a comment is about goes through
// review.Anchor.Where, so the same anchor reads the same way in the agent prompt,
// the publish preview, the publish log and the compose box.
//
// The test is on the agreement rather than on any one wording: each of these used
// to build "path:line" itself, and a scope that renders as "a.go" in the preview
// and ":" in the prompt is a scope the reader has to work out twice.
func TestEverySurfaceNamesTheSameScope(t *testing.T) {
	cases := []struct {
		name   string
		anchor review.Anchor
		want   string
	}{
		{"a line", review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 12}, "a.go:12"},
		{"a range", review.Anchor{Path: "a.go", Side: review.SideNew, LineHint: 12, EndLineHint: 18}, "a.go:12-18"},
		{"a file", review.Anchor{Path: "a.go", Side: review.SideNew}, "a.go"},
		{"the change", review.Anchor{}, "the whole change"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := review.Comment{ID: "c1", Author: review.AuthorHuman, Body: "a remark", Anchor: tc.anchor}

			// The agent prompt: the address it is told to go and read.
			if got := commentPromptFor(c, ""); !strings.Contains(got, tc.want) {
				t.Errorf("the agent prompt does not name %q:\n%s", tc.want, got)
			}

			// The publish preview. A change-scope remark is not a thread, so it is
			// previewed as a PR comment and named in the reply/summary lines instead.
			var inline []review.Comment
			if tc.anchor.Scope() != review.ChangeScope {
				inline = []review.Comment{c}
			}
			plan := strings.Join(publishPlan(
				publishRequest{PR: 7, Event: github.EventApprove, Verdict: "approve"},
				inline, nil, nil, 0, "abc123def4567", nil), "\n")
			if len(inline) > 0 && !strings.Contains(plan, "thread  "+tc.want) {
				t.Errorf("the publish preview does not name %q:\n%s", tc.want, plan)
			}

			// A reply's failure message, which is the only record of where a retry
			// should look.
			reply := c
			reply.ReplyToThread = "PRRT_1"
			replyPlan := strings.Join(publishPlan(
				publishRequest{PR: 7}, nil, nil, []review.Comment{reply}, 0, "", nil), "\n")
			if !strings.Contains(replyPlan, tc.want) {
				t.Errorf("the reply preview does not name %q:\n%s", tc.want, replyPlan)
			}
		})
	}
}

// The one thing Where must never produce: a location that trails off. Each of
// these strings goes into a sentence, and "Review comment on " followed by
// nothing reads as a broken prompt rather than as a remark about the change.
func TestNoSurfaceTrailsOffWhenThereIsNoFile(t *testing.T) {
	c := review.Comment{ID: "c1", Author: review.AuthorHuman, Body: "the whole shape is wrong"}
	got := commentPromptFor(c, "")
	first := strings.SplitN(got, "\n", 2)[0]
	if strings.HasSuffix(strings.TrimRight(first, " "), "on") || strings.Contains(first, " :") {
		t.Errorf("the prompt's address line trails off: %q", first)
	}
	if !strings.Contains(first, "the whole change") {
		t.Errorf("expected the scope named, got %q", first)
	}
}
