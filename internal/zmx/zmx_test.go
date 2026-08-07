package zmx

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/vterm"
)

// fakeZmx records the commands issued and replays canned output.
type fakeZmx struct {
	calls []string
	dirs  []string
	out   map[string]string
	fail  map[string]bool
}

func (f *fakeZmx) run(_ context.Context, dir, name string, args ...string) (string, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	f.dirs = append(f.dirs, dir)
	for sub := range f.fail {
		if strings.Contains(call, sub) {
			return "", errors.New("zmx said no")
		}
	}
	for prefix, out := range f.out {
		if strings.HasPrefix(call, prefix) {
			return out, nil
		}
	}
	return "", nil
}

func (f *fakeZmx) ran(sub string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, sub) {
			return true
		}
	}
	return false
}

// A real `zmx ls` line, tabs and all.
const lsOutput = "" +
	"  name=awp.awp.portal.agent\tpid=70963\tclients=1\tcreated=1786115519\tstart_dir=/Users/x/awp\tkind=agent\n" +
	"  name=dead\tpid=82147\tclients=0\tcreated=1786048330\tstart_dir=/Users/x\tended=1786048330\texit_code=2\n"

func TestListParsesRealZmxOutput(t *testing.T) {
	f := &fakeZmx{out: map[string]string{"zmx ls": lsOutput}}
	got, err := New(f.run).List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("parsed %d sessions from two lines: %+v", len(got), got)
	}

	live := got[0]
	if live.Name != "awp.awp.portal.agent" || live.PID != 70963 || live.Clients != 1 {
		t.Errorf("live session parsed as %+v", live)
	}
	if live.StartDir != "/Users/x/awp" {
		t.Errorf("start_dir parsed as %q", live.StartDir)
	}
	if live.Labels["kind"] != "agent" {
		t.Errorf("labels parsed as %v, want kind=agent", live.Labels)
	}
	if !live.Live() {
		t.Error("a session with no ended= reported as not live")
	}

	// A listed session is not necessarily a running one — zmx keeps finished
	// sessions around so their output can still be read.
	if dead := got[1]; dead.Live() || dead.ExitCode != 2 {
		t.Errorf("finished session parsed as %+v, want Live()=false ExitCode=2", dead)
	}
}

func TestListSaysWhatToCheckWhenZmxIsMissing(t *testing.T) {
	f := &fakeZmx{fail: map[string]bool{"zmx ls": true}}
	_, err := New(f.run).List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "PATH") {
		t.Errorf("got %v, want an error naming the thing to check", err)
	}
}

func TestEnsureReusesALiveSession(t *testing.T) {
	f := &fakeZmx{out: map[string]string{"zmx ls": lsOutput}}
	created, err := New(f.run).Ensure(context.Background(),
		"awp.awp.portal.agent", "/Users/x/awp", []string{"claude"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("Ensure created a session that was already live")
	}
	if f.ran("zmx run") {
		t.Error("Ensure started a second process for a live session")
	}
}

// A session whose command has exited must be replaced, not reused: attaching
// would render a dead program's last screen and look like a hung agent.
func TestEnsureReplacesAFinishedSession(t *testing.T) {
	f := &fakeZmx{out: map[string]string{"zmx ls": lsOutput}}
	created, err := New(f.run).Ensure(context.Background(), "dead", "/Users/x", []string{"sh"})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("Ensure reused a session whose command had exited")
	}
	if !f.ran("zmx kill dead --force") {
		t.Error("the finished session was not cleared first")
	}
	if !f.ran("zmx run dead -d sh") {
		t.Errorf("did not restart the session; calls were %v", f.calls)
	}
}

// `-d` has to come after the name. Without it zmx blocks until the command
// finishes, which hung a probe before this was understood.
func TestEnsureStartsDetachedInTheRightDirectory(t *testing.T) {
	f := &fakeZmx{}
	if _, err := New(f.run).Ensure(context.Background(),
		"awp.p.w.shell", "/repo/path", []string{"fish", "-l"}); err != nil {
		t.Fatal(err)
	}
	if !f.ran("zmx run awp.p.w.shell -d fish -l") {
		t.Errorf("wrong invocation: %v", f.calls)
	}
	var startDir string
	for i, c := range f.calls {
		if strings.HasPrefix(c, "zmx run") {
			startDir = f.dirs[i]
		}
	}
	if startDir != "/repo/path" {
		t.Errorf("the session was started in %q, want /repo/path", startDir)
	}
}

func TestEnsureRefusesNonsense(t *testing.T) {
	c := New((&fakeZmx{}).run)
	if _, err := c.Ensure(context.Background(), "", "/d", []string{"sh"}); err == nil {
		t.Error("an empty name was accepted")
	}
	if _, err := c.Ensure(context.Background(), "n", "/d", nil); err == nil {
		t.Error("an empty command was accepted")
	}
}

// zmx silently declines to create a session whose name contains a slash —
// measured, not guessed — so SessionName must never emit one.
func TestSessionNameIsSafeForZmx(t *testing.T) {
	for _, tc := range []struct{ project, workspace, kind, want string }{
		{"awp", "portal", "agent", "awp.awp.portal.agent"},
		{"pipelines", "back_fill", "shell", "awp.pipelines.back_fill.shell"},
		{"a/b", "c d", "vcs", "awp.a_b.c_d.vcs"},
		{"", "", "", "awp._._._"},
	} {
		if got := SessionName(tc.project, tc.workspace, tc.kind); got != tc.want {
			t.Errorf("SessionName(%q,%q,%q) = %q, want %q",
				tc.project, tc.workspace, tc.kind, got, tc.want)
		}
	}
	for _, bad := range []string{"a/b", "x\ty", "p q"} {
		name := SessionName(bad, bad, bad)
		if strings.ContainsAny(name, "/\t ") {
			t.Errorf("SessionName produced %q, which zmx will reject", name)
		}
	}
}

// Both kinds of hosted process — an attached session and a directly spawned
// one — must be told they are talking to the emulator, not to whatever awp is
// running under.
func TestHostedCommandsDescribeTheEmulator(t *testing.T) {
	base := []string{"TERM=tmux-256color", "TMUX=/tmp/x,1,0", "PATH=/bin"}
	for _, tc := range []struct {
		name string
		cmd  interface{ Environ() []string }
	}{
		{"attach", AttachCmd("awp.p.w.agent", base)},
		{"direct", Command("/repo", []string{"jjui"}, base)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			terms := 0
			for _, kv := range tc.cmd.Environ() {
				if strings.HasPrefix(kv, "TMUX=") {
					t.Error("the hosted process inherited TMUX")
				}
				if strings.HasPrefix(kv, "TERM=") {
					terms++
					if kv != "TERM="+vterm.TermType {
						t.Errorf("got %q, want TERM=%s", kv, vterm.TermType)
					}
				}
			}
			if terms != 1 {
				t.Errorf("%d TERM entries, want exactly 1", terms)
			}
		})
	}
}

func TestCommandRunsInTheWorkspace(t *testing.T) {
	cmd := Command("/repo/path", []string{"jjui"}, nil)
	if cmd.Dir != "/repo/path" {
		t.Errorf("an ephemeral pane would run in %q, want /repo/path", cmd.Dir)
	}
}
