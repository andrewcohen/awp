package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AWP_PPROF names the file the CPU profile is written to. It also reads like a
// switch, and every plausible spelling of "on" is a valid filename — which is
// how a 26 KB profile ended up in a file called `true` at the repo root, and
// then in a commit.

// TestAProfileSwitchIsNotAFilename. The refusal is what makes the mistake
// visible; without it the profile lands in ./true and nothing says so.
func TestAProfileSwitchIsNotAFilename(t *testing.T) {
	for _, on := range []string{"1", "true", "TRUE", "yes", "on", "y"} {
		t.Setenv("AWP_PPROF", on)
		stop, err := startDeckProfile()
		if stop != nil {
			stop()
			t.Errorf("AWP_PPROF=%s started a profile", on)
		}
		if err == nil {
			t.Errorf("AWP_PPROF=%s was accepted as a filename", on)
			continue
		}
		// The reader has to be able to act on it, which means being told the
		// variable wants a path and shown one.
		for _, want := range []string{"AWP_PPROF", "path"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("AWP_PPROF=%s error omits %q: %v", on, want, err)
			}
		}
		if _, statErr := os.Stat(on); statErr == nil {
			t.Errorf("AWP_PPROF=%s created a file named %q", on, on)
		}
	}
}

// TestOffMeansOff. Someone who writes AWP_PPROF=false wants no profile, and
// the one outcome they must not get is a profile in ./false. Nothing to
// report either — they got what they asked for.
func TestOffMeansOff(t *testing.T) {
	for _, off := range []string{"0", "false", "FALSE", "no", "off", "n"} {
		t.Setenv("AWP_PPROF", off)
		stop, err := startDeckProfile()
		if stop != nil {
			stop()
			t.Errorf("AWP_PPROF=%s started a profile", off)
		}
		if err != nil {
			t.Errorf("AWP_PPROF=%s complained: %v", off, err)
		}
		if _, statErr := os.Stat(off); statErr == nil {
			t.Errorf("AWP_PPROF=%s created a file named %q", off, off)
		}
	}
}

// TestAPathStillProfiles — the guard must not have cost the feature it
// protects. A real path is still a real path, boolean-looking basename or not.
func TestAPathStillProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "true")
	t.Setenv("AWP_PPROF", path)
	stop, err := startDeckProfile()
	if err != nil {
		t.Fatalf("a path was refused: %v", err)
	}
	if stop == nil {
		t.Fatal("a path started no profile")
	}
	stop()
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("no profile at %s: %v", path, statErr)
	}
	if info.Size() == 0 {
		t.Errorf("the profile at %s is empty", path)
	}
}

// TestNoProfileByDefault: unset means the deck does no profiling work at all.
func TestNoProfileByDefault(t *testing.T) {
	t.Setenv("AWP_PPROF", "")
	stop, err := startDeckProfile()
	if err != nil || stop != nil {
		t.Errorf("unset AWP_PPROF: stop=%v err=%v, want nil/nil", stop != nil, err)
	}
}
