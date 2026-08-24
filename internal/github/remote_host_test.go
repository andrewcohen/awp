package github

import (
	"context"
	"testing"
)

// Where a repo is hosted decides whether asking gh about it is worth a
// subprocess. awp's projects are not all on GitHub, and a repo on GitLab used to
// cost four failed gh calls a minute — see fetchRepoPRStatus.

// urlRunner answers `git remote get-url origin` with a fixed string.
type urlRunner struct {
	url string
	err error
}

func (r urlRunner) Run(_ context.Context, _ string, _ string, _ ...string) (string, error) {
	return r.url, r.err
}

func TestOriginHostReadsBothShapesGitWrites(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"git@github.com:andrewcohen/awp.git\n", "github.com"},
		{"git@gitlab.com:fastgrowingtrees/harbor-works.git", "gitlab.com"},
		{"git@bitbucket.org:tbkinc/fgt-shopify.git", "bitbucket.org"},
		{"ssh://git@github.com/andrewcohen/fgt-remix-poc.git", "github.com"},
		{"https://github.com/andrewcohen/awp", "github.com"},
		{"https://user:token@gitlab.example.com:8443/team/repo.git", "gitlab.example.com"},
		{"https://GitHub.com/Andrewcohen/Awp", "github.com"},
		// A path is a repo with nowhere to ask about it, not a host.
		{"/Users/acohen/p/local-clone", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := New(urlRunner{url: c.url}, "/repo").OriginHost()
		if got != c.want {
			t.Errorf("OriginHost(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// A repo with no origin at all — git exits non-zero — is not an error to report
// anywhere. It is a repo gh cannot serve, which is the only thing the caller asks.
func TestARepoWithNoOriginIsNotOnGitHub(t *testing.T) {
	c := New(urlRunner{err: context.Canceled}, "/repo")
	if host := c.OriginHost(); host != "" {
		t.Errorf("OriginHost with no origin = %q, want empty", host)
	}
	if c.OnGitHub() {
		t.Error("a repo with no origin reported as being on GitHub")
	}
}
