package zmx

import "testing"

func TestSessionNameRoundTrips(t *testing.T) {
	for _, tc := range []struct{ project, workspace, kind string }{
		{"awp", "test", "agent"},
		{"alpha", "bump-deps", "editor"},
		{"a_b", "c-d", "shell"},
	} {
		name := SessionName(tc.project, tc.workspace, tc.kind)
		p, w, k, ok := ParseSessionName(name)
		if !ok || p != tc.project || w != tc.workspace || k != tc.kind {
			t.Errorf("%q -> (%q,%q,%q,%v), want (%q,%q,%q,true)",
				name, p, w, k, ok, tc.project, tc.workspace, tc.kind)
		}
	}
}

// zmx ls lists every session on the machine. Anything not ours must be
// recognisable as such rather than mangled into three empty parts.
func TestForeignSessionNamesAreRejected(t *testing.T) {
	for _, name := range []string{"", "scratch", "awp.only.three", "awp.a.b.c.d", "other.a.b.c"} {
		if _, _, _, ok := ParseSessionName(name); ok {
			t.Errorf("%q was accepted as an awp session", name)
		}
	}
}

func TestCreatedAndCmdAreParsed(t *testing.T) {
	line := "name=awp.p.w.agent\tpid=42\tclients=1\tcreated=1786125121\tstart_dir=/tmp\tcmd=claude --model opus"
	s, ok := parseSession(line)
	if !ok {
		t.Fatal("the line did not parse")
	}
	if s.Cmd != "claude --model opus" {
		t.Errorf("cmd = %q", s.Cmd)
	}
	if s.Created.Unix() != 1786125121 {
		t.Errorf("created = %v", s.Created)
	}
	if _, isLabel := s.Labels["cmd"]; isLabel {
		t.Error("cmd leaked into Labels, where it reads as a user-set label")
	}
}
