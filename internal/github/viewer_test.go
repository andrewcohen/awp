package github

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// A PR requested from a team and nobody else. This is the shape that started
// this: two Team nodes, zero User nodes, and a reviewer who is in one of the
// teams. Read by login alone the PR looks like it wants nothing from anybody.
const teamRequestJSON = `{"number":557,"headRefName":"feat/x","state":"OPEN",
	"reviewRequests":[
		{"__typename":"Team","name":"Consumer Team","slug":"acme-corp/consumer-team"},
		{"__typename":"Team","name":"Enterprise Team","slug":"acme-corp/enterprise-team"}]}`

// TestATeamRequestSurvivesTheProjection. The raw node has no login, so the
// old shape dropped it on the floor and there was nothing downstream to read.
func TestATeamRequestSurvivesTheProjection(t *testing.T) {
	var r rawPRStatus
	if err := json.Unmarshal([]byte(teamRequestJSON), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := r.requestedLogins(); len(got) != 0 {
		t.Errorf("a team request produced logins %q — a Team node has none", got)
	}
	want := []string{"acme-corp/consumer-team", "acme-corp/enterprise-team"}
	if got := r.requestedTeams(); !reflect.DeepEqual(got, want) {
		t.Errorf("requested teams = %q, want %q", got, want)
	}
}

// TestAUserRequestIsStillOnlyALogin — the two kinds share a list, and each
// belongs on exactly one side of the split.
func TestAUserRequestIsStillOnlyALogin(t *testing.T) {
	var r rawPRStatus
	body := `{"reviewRequests":[{"__typename":"User","login":"andrewcohen"}]}`
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := r.requestedLogins(); !reflect.DeepEqual(got, []string{"andrewcohen"}) {
		t.Errorf("requested logins = %q", got)
	}
	if got := r.requestedTeams(); len(got) != 0 {
		t.Errorf("a user request produced teams %q — a User node has no slug", got)
	}
}

// TestTheViewersTeamMakesTheRequestTheirs is the whole point: nobody named
// the viewer, and the PR is still waiting on them.
func TestTheViewersTeamMakesTheRequestTheirs(t *testing.T) {
	pr := PRStatus{ReviewRequestTeams: []string{"acme-corp/consumer-team", "acme-corp/enterprise-team"}}
	v := Viewer{Login: "andrewcohen", Teams: []string{"acme-corp/consumer-team"}}
	if !v.ReviewRequested(pr) {
		t.Error("a review requested from the viewer's own team does not read as requested")
	}
}

// TestSomeoneElsesTeamIsNotYourRequest. The signal drives the attention
// scope, so a false positive puts a PR nobody asked the viewer about on the
// list of things that want them.
func TestSomeoneElsesTeamIsNotYourRequest(t *testing.T) {
	pr := PRStatus{ReviewRequestTeams: []string{"acme-corp/enterprise-team"}}
	v := Viewer{Login: "andrewcohen", Teams: []string{"acme-corp/consumer-team"}}
	if v.ReviewRequested(pr) {
		t.Error("a team the viewer is not in reads as their request")
	}
}

// TestTwoOrgsCanShareATeamName, and a request in one is not a request in the
// other. This is why the unqualified fallback only relaxes the requested
// side: bare-slug matching in both directions would hand the viewer review
// requests from an org they have no standing in.
func TestTwoOrgsCanShareATeamName(t *testing.T) {
	pr := PRStatus{ReviewRequestTeams: []string{"other-org/platform-team"}}
	v := Viewer{Login: "andrewcohen", Teams: []string{"acme-corp/platform-team"}}
	if v.ReviewRequested(pr) {
		t.Error("another org's identically-named team reads as the viewer's")
	}
}

// TestAnUnqualifiedRequestStillMatches. The field is documented as a slug,
// and nothing promises the org qualification — one fixture in this package
// has a bare one. Matching the slug alone is the fallback.
func TestAnUnqualifiedRequestStillMatches(t *testing.T) {
	pr := PRStatus{ReviewRequestTeams: []string{"platform-team"}}
	v := Viewer{Login: "andrewcohen", Teams: []string{"acme-corp/platform-team"}}
	if !v.ReviewRequested(pr) {
		t.Error("an unqualified request does not match the viewer's team")
	}
}

// TestCaseIsNotIdentity — GitHub logins and slugs are both case-insensitive.
func TestCaseIsNotIdentity(t *testing.T) {
	pr := PRStatus{
		Author:             "AndrewCohen",
		ReviewRequests:     []string{"SomebodyElse"},
		ReviewRequestTeams: []string{"Acme-Corp/Consumer-Team"},
		Reviewers:          []string{"ANDREWCOHEN"},
	}
	v := Viewer{Login: "andrewcohen", Teams: []string{"acme-corp/consumer-team"}}
	if !v.Authored(pr) {
		t.Error("Authored is case-sensitive")
	}
	if !v.ReviewRequested(pr) {
		t.Error("a team request is case-sensitive")
	}
	if !v.Reviewed(pr) {
		t.Error("Reviewed is case-sensitive")
	}
}

// TestAnUnknownViewerIsRelativeToNobody. The login lookup is best-effort and
// the review write-through has no login to offer, so an empty viewer has to
// mean "leave the viewer-relative signals off" rather than "match anything".
func TestAnUnknownViewerIsRelativeToNobody(t *testing.T) {
	pr := PRStatus{
		Author:             "",
		ReviewRequests:     []string{""},
		ReviewRequestTeams: []string{"acme-corp/consumer-team"},
		Reviewers:          []string{""},
	}
	// Teams without a login should not carry the day either — they are read
	// with the same credential, so one without the other is not an identity.
	v := Viewer{Teams: []string{"acme-corp/consumer-team"}}
	if v.Known() {
		t.Error("a viewer with no login reads as known")
	}
	if v.Authored(pr) || v.ReviewRequested(pr) || v.Reviewed(pr) || v.ReviewRerequested(pr) {
		t.Error("an unknown viewer matched an empty PR")
	}
}

// TestARerequestIsARequestPlusAReview, over a team request too: the author
// asked the team again after the viewer already reviewed.
func TestARerequestIsARequestPlusAReview(t *testing.T) {
	v := Viewer{Login: "andrewcohen", Teams: []string{"acme-corp/consumer-team"}}
	asked := PRStatus{ReviewRequestTeams: []string{"acme-corp/consumer-team"}}
	if v.ReviewRerequested(asked) {
		t.Error("a first request reads as a re-request")
	}
	again := asked
	again.Reviewers = []string{"andrewcohen"}
	if !v.ReviewRerequested(again) {
		t.Error("asked again after reviewing does not read as a re-request")
	}
	reviewedUnasked := PRStatus{Reviewers: []string{"andrewcohen"}}
	if v.ReviewRerequested(reviewedUnasked) {
		t.Error("a review with no outstanding request reads as a re-request")
	}
}

// TestViewerTeamsAreOrgQualified. The API reports the org and the slug in
// separate fields; requests use them joined, so the join happens once, here.
func TestViewerTeamsAreOrgQualified(t *testing.T) {
	r := &fakeRunner{out: "acme-corp/consumer-team\nacme-corp/platform-team\n"}
	got, err := New(r, "/repos/x").ViewerTeams()
	if err != nil {
		t.Fatalf("ViewerTeams err: %v", err)
	}
	if want := []string{"acme-corp/consumer-team", "acme-corp/platform-team"}; !reflect.DeepEqual(got, want) {
		t.Errorf("teams = %q, want %q", got, want)
	}
	joined := r.gotName + " " + strings.Join(r.gotArgs, " ")
	for _, want := range []string{"user/teams", "--paginate", ".organization.login", ".slug"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in the call, got %q", want, joined)
		}
	}
}

// TestNoTeamsIsNotAnError. Plenty of accounts are in none, and the answer is
// an empty list rather than a failure the caller has to interpret.
func TestNoTeamsIsNotAnError(t *testing.T) {
	got, err := New(&fakeRunner{out: "\n"}, "/repos/x").ViewerTeams()
	if err != nil {
		t.Fatalf("ViewerTeams err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("teams = %q, want none", got)
	}
}

// TestUnreadableTeamsSayHowToFixIt. The call needs read:org, which
// `gh auth login` does not grant by default, so the most likely failure has
// a one-line remedy and the error should carry it.
func TestUnreadableTeamsSayHowToFixIt(t *testing.T) {
	r := &fakeRunner{err: errors.New("exit 1"), out: "HTTP 403: Resource not accessible"}
	_, err := New(r, "/repos/x").ViewerTeams()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "read:org") {
		t.Errorf("error does not name the missing scope: %v", err)
	}
}
