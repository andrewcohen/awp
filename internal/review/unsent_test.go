package review

import "testing"

// TestUnsentIsYourOwnWordsStillAwaitingTriage.
//
// The robot case is the one worth the test on its own: an agent's finding is Local
// and Open exactly like yours, because Open means "awaiting triage" and it is
// awaiting *yours*. Handing those back to the agent that filed them would look
// like the feature working, and would answer a question with itself.
func TestUnsentIsYourOwnWordsStillAwaitingTriage(t *testing.T) {
	mine := func(c Comment) Comment {
		c.Author = AuthorHuman
		if c.State == "" {
			c.State = Open
		}
		return c
	}

	cases := []struct {
		name string
		c    Comment
		want bool
	}{
		{"a remark you wrote and have not sent", mine(Comment{ID: "c1", Body: "why here?"}), true},
		{"a reply of yours into a GitHub thread, not posted", mine(Comment{ID: "c2", ReplyToThread: "PRRT_1"}), true},
		{"one you have already handed over", mine(Comment{ID: "c3", State: Sent}), false},
		{"one the agent has since addressed", mine(Comment{ID: "c4", State: Addressed}), false},
		{"one that is on GitHub", mine(Comment{ID: "c5", State: Published}), false},
		{"one whose publish record says it is on GitHub", mine(Comment{ID: "c6", Publish: &PublishRecord{ThreadID: "PRRT_2"}}), false},
		{"an agent's finding, awaiting your triage", Comment{ID: "c7", Author: "claude", State: Open}, false},
		{"GitHub's own record of someone else's comment", Comment{ID: RemoteThreadID("PRRT_3"), Author: "alice", State: Open}, false},
	}
	for _, tc := range cases {
		if got := tc.c.Unsent(); got != tc.want {
			t.Errorf("%s: Unsent() = %v, want %v", tc.name, got, tc.want)
		}
	}
}
