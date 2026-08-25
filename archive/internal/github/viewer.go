package github

import "strings"

// Viewer is who a PR is being read on behalf of: the authenticated login,
// plus the teams that login belongs to. Both halves are needed because a
// review request can name either one, and a PR requested from a team names
// no individual at all — read by login alone it looks like a PR that wants
// nothing from anybody.
//
// The viewer-relative questions live here as methods rather than at the
// projection that consumes them, so "is this request mine" has one spelling
// next to the fields that explain it. An unknown viewer (the login lookup
// failed, or the caller has no login to offer) answers no to all of them,
// which leaves every viewer-relative signal off rather than guessing.
type Viewer struct {
	// Login is the authenticated user's GitHub login, or "" if unknown.
	Login string
	// Teams is the org-qualified slugs of the login's teams, as
	// ViewerTeams reports them. Empty is normal — a token without the
	// read:org scope cannot read them, and plenty of repos never request a
	// team's review.
	Teams []string
}

// Known reports whether there is a viewer to be relative to. Without a
// login there is nobody to compare against, and a PR requested from a team
// the process cannot attribute to anyone is not evidence about this user.
func (v Viewer) Known() bool { return strings.TrimSpace(v.Login) != "" }

// Authored reports whether the viewer opened s.
func (v Viewer) Authored(s PRStatus) bool {
	return v.Known() && strings.EqualFold(s.Author, v.Login)
}

// ReviewRequested reports whether s asks the viewer for a review — by name,
// or through a team they are in. GitHub puts a reviewer back in the request
// set when the author asks again, so this covers a first request and a
// re-request alike; Reviewed is what separates them.
func (v Viewer) ReviewRequested(s PRStatus) bool {
	if !v.Known() {
		return false
	}
	if containsFold(s.ReviewRequests, v.Login) {
		return true
	}
	for _, requested := range s.ReviewRequestTeams {
		for _, mine := range v.Teams {
			if teamMatches(requested, mine) {
				return true
			}
		}
	}
	return false
}

// Reviewed reports whether the viewer has a review on record for s.
func (v Viewer) Reviewed(s PRStatus) bool {
	return v.Known() && containsFold(s.Reviewers, v.Login)
}

// ReviewRerequested reports whether the viewer has been asked to review s
// AGAIN: they are in the request set and already have a review on record.
func (v Viewer) ReviewRerequested(s PRStatus) bool {
	return v.ReviewRequested(s) && v.Reviewed(s)
}

// teamMatches reports whether a requested team slug names one of the
// viewer's teams. Both sides are compared org-qualified, which is how
// GitHub reports a request ("acme-corp/consumer-team") and how ViewerTeams
// builds the viewer's own.
//
// An unqualified request falls back to matching the slug alone, because the
// field is documented as a slug and nothing guarantees the qualification —
// but only in that direction. Comparing bare slugs both ways would make one
// org's platform-team match another's, and a PR in a repo the viewer has no
// standing in would start asking them for review.
func teamMatches(requested, mine string) bool {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return false
	}
	if strings.EqualFold(requested, mine) {
		return true
	}
	if strings.Contains(requested, "/") {
		return false
	}
	_, slug, ok := strings.Cut(mine, "/")
	return ok && strings.EqualFold(requested, slug)
}

// containsFold reports whether want appears in have, case-insensitively
// (GitHub logins and slugs are both case-insensitive).
func containsFold(have []string, want string) bool {
	for _, h := range have {
		if strings.EqualFold(h, want) {
			return true
		}
	}
	return false
}
