package cli

import (
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

func TestFormatUnreadSummary(t *testing.T) {
	tests := []struct {
		name    string
		all     map[string]map[string]workspace.Entry
		want    string
		wantEmpty bool
	}{
		{
			name:      "nothing to show",
			all:       map[string]map[string]workspace.Entry{},
			wantEmpty: true,
		},
		{
			name: "idle-but-read agents stay silent",
			all: map[string]map[string]workspace.Entry{
				"/r": {"a": {Status: "idle", Unread: false}},
			},
			wantEmpty: true,
		},
		{
			name: "working counts regardless of unread",
			all: map[string]map[string]workspace.Entry{
				"/r": {
					"a": {Status: "working", Unread: false},
					"b": {Status: "running", Unread: true},
				},
			},
			want: "#[fg=green]● 2#[default]",
		},
		{
			name: "all three buckets in order",
			all: map[string]map[string]workspace.Entry{
				"/r": {
					"work": {Status: "working"},
					"wait": {Status: "waiting", Unread: true},
					"done": {Status: "idle", Unread: true},
				},
			},
			want: "#[fg=green]● 1#[default]  #[fg=yellow]▲ 1#[default]  ● 1",
		},
		{
			name: "waiting without unread is not counted",
			all: map[string]map[string]workspace.Entry{
				"/r": {"a": {Status: "waiting", Unread: false}},
			},
			wantEmpty: true,
		},
		{
			name: "working wins over a stale unread flag (no double count)",
			all: map[string]map[string]workspace.Entry{
				"/r": {"a": {Status: "working", Unread: true}},
			},
			want: "#[fg=green]● 1#[default]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatUnreadSummary(tt.all)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("want empty, got %q", got)
				}
				return
			}
			if got != tt.want {
				t.Errorf("formatUnreadSummary() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

func TestIsWorkingStatus(t *testing.T) {
	for _, s := range []string{"working", "Working", " running ", "in progress", "in_progress"} {
		if !isWorkingStatus(s) {
			t.Errorf("isWorkingStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"idle", "waiting", "exited", "", "done"} {
		if isWorkingStatus(s) {
			t.Errorf("isWorkingStatus(%q) = true, want false", s)
		}
	}
}
