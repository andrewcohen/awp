package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/review"
	"github.com/andrewcohen/awp/internal/workspace"
)

// proposalCLI is a review with one finding in it, plus the runner and service the
// review subcommands resolve through.
func proposalCLI(t *testing.T) (rootRunner, workspace.Service, review.Comment) {
	t.Helper()
	root := tempRoot(t)
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "default", Path: root}}}
	chdir(t, root)
	runner := rootRunner{root: root}

	var out bytes.Buffer
	if err := runReviewAdd(runner, svc, []string{"--file", "a.go", "--line", "12", "--body", "this drops the error"}, &out); err != nil {
		t.Fatalf("review add: %v", err)
	}
	return runner, svc, listComments(t, runner, svc)[0]
}

// listComments reads the review back through the machine channel.
func listComments(t *testing.T, runner rootRunner, svc workspace.Service) []review.Comment {
	t.Helper()
	var out bytes.Buffer
	if err := runReviewList(runner, svc, []string{"--json"}, &out); err != nil {
		t.Fatalf("review list --json: %v", err)
	}
	var got []review.Comment
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("review list --json: %v (%q)", err, out.String())
	}
	return got
}

// find returns the comment answering parent, which is the reply just filed.
func replyTo(t *testing.T, comments []review.Comment, parent string) review.Comment {
	t.Helper()
	for _, c := range comments {
		if c.ReplyTo == parent {
			return c
		}
	}
	t.Fatalf("no reply to %s in %d comments", parent, len(comments))
	return review.Comment{}
}

// --proposal files the reply as an offer awaiting a yes. This is the agent's half
// of the gate the prompt sets up: reply before changing anything, then stop.
func TestReplyProposalFilesAnOfferAwaitingApproval(t *testing.T) {
	runner, svc, finding := proposalCLI(t)

	var out bytes.Buffer
	err := runReviewReply(runner, svc, []string{"--to", finding.ID, "--proposal", "--body", "wrap it in m.fail and return early"}, &out)
	if err != nil {
		t.Fatalf("review reply --proposal: %v", err)
	}
	// The agent that just ran the command has to be told the two replies differ:
	// an ordinary one is said and done, this one means stop.
	if !strings.Contains(out.String(), "awaiting approval") {
		t.Errorf("the confirmation does not say it is waiting on anyone: %q", out.String())
	}

	got := replyTo(t, listComments(t, runner, svc), finding.ID)
	if !got.AwaitingApproval() {
		t.Errorf("the filed reply is %q, want pending", got.Proposal)
	}
}

// Without the flag a reply is an ordinary reply. The gate is about changing code,
// not about replying: an agent answering a question or explaining why the code is
// the way it is needs no approval.
func TestAReplyWithoutTheFlagIsNotAProposal(t *testing.T) {
	runner, svc, finding := proposalCLI(t)

	var out bytes.Buffer
	if err := runReviewReply(runner, svc, []string{"--to", finding.ID, "--body", "it is intentional — the caller logs it"}, &out); err != nil {
		t.Fatalf("review reply: %v", err)
	}
	if strings.Contains(out.String(), "approval") {
		t.Errorf("a plain reply claims to be waiting on approval: %q", out.String())
	}

	got := replyTo(t, listComments(t, runner, svc), finding.ID)
	if got.IsProposal() {
		t.Errorf("a plain reply was filed as a proposal (%q)", got.Proposal)
	}
}

// `review list` is where an agent finds out, since there is no dedicated query.
// A row has to say which of the three it is: not a proposal, waiting, or go.
func TestReviewListSaysWhereAProposalStands(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	var out bytes.Buffer
	if err := runReviewReply(runner, svc, []string{"--to", finding.ID, "--proposal", "--body", "wrap it in m.fail"}, &out); err != nil {
		t.Fatalf("review reply --proposal: %v", err)
	}

	pending := listing(t, runner, svc)
	if !strings.Contains(pending, "pending") {
		t.Fatalf("the listing does not say the proposal is pending:\n%s", pending)
	}
	// Every row carries the column, so the fields line up and a reader can index
	// them — the finding itself is not a proposal and says so.
	for _, line := range strings.Split(strings.TrimSpace(pending), "\n") {
		if !strings.HasPrefix(line, finding.ID) {
			continue
		}
		if fields := strings.Split(line, "\t"); len(fields) < 4 || fields[3] != "-" {
			t.Errorf("the finding's proposal column is %q, want -: %q", fields, line)
		}
	}

	// And once approved it says so, which is the whole answer to "may I proceed".
	store, r := reviewFor(t, runner, svc)
	proposal := replyTo(t, listComments(t, runner, svc), finding.ID)
	if _, err := store.Approve(r, proposal.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved := listing(t, runner, svc); !strings.Contains(approved, "approved") {
		t.Fatalf("the listing does not say the proposal was approved:\n%s", approved)
	}
}

// listing is `review list` in its human form.
func listing(t *testing.T, runner rootRunner, svc workspace.Service) string {
	t.Helper()
	var out bytes.Buffer
	if err := runReviewList(runner, svc, nil, &out); err != nil {
		t.Fatalf("review list: %v", err)
	}
	return out.String()
}

// reviewFor resolves the same review the subcommands write to, so a test can act
// on the store directly where no command exists yet.
func reviewFor(t *testing.T, runner rootRunner, svc workspace.Service) (review.Store, review.Review) {
	t.Helper()
	scope, err := openReviewFor(runner, svc, "")
	if err != nil {
		t.Fatalf("open review: %v", err)
	}
	return scope.store, scope.review
}

// The machine channel carries it too. An agent parsing --json must not have to
// fall back to scraping the human table for the one field it is waiting on.
func TestProposalStateSurvivesTheJSONChannel(t *testing.T) {
	runner, svc, finding := proposalCLI(t)
	var out bytes.Buffer
	if err := runReviewReply(runner, svc, []string{"--to", finding.ID, "--proposal", "--body", "wrap it"}, &out); err != nil {
		t.Fatalf("review reply --proposal: %v", err)
	}

	var raw []map[string]any
	var buf bytes.Buffer
	if err := runReviewList(runner, svc, []string{"--json"}, &buf); err != nil {
		t.Fatalf("review list --json: %v", err)
	}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var found bool
	for _, c := range raw {
		if c["proposal"] == "pending" {
			found = true
		}
		// Omitted rather than written empty on everything that is not one, so an
		// existing record and a new plain reply read identically.
		if c["reply_to"] == nil {
			if _, ok := c["proposal"]; ok {
				t.Errorf("a plain finding carries a proposal field: %v", c)
			}
		}
	}
	if !found {
		t.Errorf("no comment came back marked pending: %v", raw)
	}
}
