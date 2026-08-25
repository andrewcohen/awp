package deckui

import "testing"

func TestFormatDevLoopMeta(t *testing.T) {
	tests := []struct {
		name string
		in   DevLoopSummary
		want string
	}{
		{
			name: "progress phase and task",
			in:   DevLoopSummary{Done: 3, Total: 7, Phase: "implement", Task: "wire up meta line"},
			want: "3/7 · implement · ▶ wire up meta line",
		},
		{
			name: "no todo list falls back to phase and task",
			in:   DevLoopSummary{Phase: "explore", Task: "read the loader"},
			want: "explore · ▶ read the loader",
		},
		{
			name: "count and phase only when no unit is in progress",
			in:   DevLoopSummary{Done: 2, Total: 5, Phase: "gates"},
			want: "2/5 · gates",
		},
		{
			name: "whitespace-only task is dropped",
			in:   DevLoopSummary{Done: 1, Total: 2, Task: "   "},
			want: "1/2",
		},
		{
			name: "empty summary renders nothing (falls through to normal meta)",
			in:   DevLoopSummary{},
			want: "",
		},
		{
			name: "all units done renders nothing (back to default behavior)",
			in:   DevLoopSummary{Done: 12, Total: 12, Phase: "commit"},
			want: "",
		},
		{
			name: "done exceeding total is still treated as finished",
			in:   DevLoopSummary{Done: 5, Total: 4},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatDevLoopMeta(tt.in); got != tt.want {
				t.Errorf("formatDevLoopMeta(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// metaLine returns the dev-loop summary in place of the port/branch meta
// when the item carries progress, and falls through to the normal meta
// line when DevLoop is nil.
func TestMetaLineUsesDevLoopSummary(t *testing.T) {
	m := Model{}
	base := Item{ProjectName: "awp", WorkspaceName: "ws", HeadChangeID: "abc123"}

	withLoop := base
	withLoop.DevLoop = &DevLoopSummary{Done: 1, Total: 4, Phase: "test", Task: "add coverage"}
	if got, want := m.metaLine(withLoop), "1/4 · test · ▶ add coverage"; got != want {
		t.Errorf("metaLine with DevLoop = %q, want %q", got, want)
	}

	if got := m.metaLine(base); got == "" || got == "1/4 · test · ▶ add coverage" {
		t.Errorf("metaLine without DevLoop should be the standard meta line, got %q", got)
	}

	// An empty (all-zero) summary carries nothing to show, so metaLine must
	// fall through to the standard meta line rather than blanking the row.
	empty := base
	empty.DevLoop = &DevLoopSummary{}
	if got, std := m.metaLine(empty), m.metaLine(base); got != std {
		t.Errorf("metaLine with empty DevLoop = %q, want standard meta %q", got, std)
	}

	// A finished loop (all units done) also falls through to the standard
	// meta line — no lingering "12/12" once the work is complete.
	done := base
	done.DevLoop = &DevLoopSummary{Done: 12, Total: 12, Phase: "commit"}
	if got, std := m.metaLine(done), m.metaLine(base); got != std {
		t.Errorf("metaLine with all-done DevLoop = %q, want standard meta %q", got, std)
	}
}
