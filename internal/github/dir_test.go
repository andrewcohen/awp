package github

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Which repository a gh call is about must come from the client, on every method.
//
// This is the invariant the three-mechanism mess broke. When the directory could be
// said as a runner wrapper, an In(dir) client, or a per-method repoDir argument, a
// new method could plausibly use none of them and quietly address whatever repo the
// process was started in — which is what the review path did, 404ing on a good day
// and writing to the wrong PR on a bad one. Now there is one place it can come
// from, and this walks every exported method to check none of them forgot.

// dirRecorder records the directory of every command it is asked to run.
type dirRecorder struct {
	mu   sync.Mutex
	dirs []string
}

func (d *dirRecorder) Run(_ context.Context, dir string, _ string, _ ...string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dirs = append(d.dirs, dir)
	// Nothing parses as a valid answer, so each method fails right after its Run —
	// which is all this test needs. It is asking where the call went, not what came
	// back.
	return "", nil
}

// zeroArg is a usable argument of type t, for calling a method by reflection.
//
// The values are chosen to get *past* each method's own validation, because a
// method that returns before running anything records no call — and this test would
// then pass on it without having checked it. Numbers are non-zero for the PR-number
// checks, and strings are a review event because the two submit methods reject
// anything else; every other string parameter is an opaque id, so the value is
// arbitrary there.
func zeroArg(t reflect.Type) reflect.Value {
	switch t.Kind() {
	case reflect.Int:
		return reflect.ValueOf(7)
	case reflect.String:
		return reflect.ValueOf(EventApprove)
	case reflect.Bool:
		return reflect.ValueOf(true)
	default:
		return reflect.Zero(t)
	}
}

func TestEveryGHCallRunsInTheClientsDirectory(t *testing.T) {
	const sentinel = "/repos/the-one-the-caller-meant"

	client := reflect.TypeOf(&Client{})
	var called, skipped []string
	for i := 0; i < client.NumMethod(); i++ {
		m := client.Method(i)
		rec := &dirRecorder{}
		// A fresh client per method, so one method's calls cannot be mistaken for
		// another's.
		c := reflect.ValueOf(New(rec, sentinel))
		args := make([]reflect.Value, 0, m.Type.NumIn())
		args = append(args, c)
		for a := 1; a < m.Type.NumIn(); a++ {
			args = append(args, zeroArg(m.Type.In(a)))
		}
		if m.Type.IsVariadic() {
			// Reflect wants Call on a variadic method to receive the slice's elements;
			// none of these methods are variadic today, so this is a guard rather than a
			// path.
			skipped = append(skipped, m.Name+" (variadic)")
			continue
		}
		func() {
			// A method that panics on a garbage answer is not what is under test here;
			// what it did with the directory before panicking still counts.
			defer func() { _ = recover() }()
			m.Func.Call(args)
		}()

		if len(rec.dirs) == 0 {
			// Validated its arguments and returned before running anything. Recorded so
			// the coverage this test claims is visible rather than assumed.
			skipped = append(skipped, m.Name)
			continue
		}
		called = append(called, m.Name)
		for _, dir := range rec.dirs {
			if dir != sentinel {
				t.Errorf("%s ran gh in %q, not the client's directory — "+
					"take the directory from c.dir, never from the runner or an argument",
					m.Name, dir)
			}
		}
	}

	// The test has to have actually reached the runner for most of the surface,
	// or a refactor that stopped calling gh entirely would look like a pass.
	if len(called) < 10 {
		t.Fatalf("only %d method(s) reached the runner (%s); skipped: %s",
			len(called), strings.Join(called, ", "), strings.Join(skipped, ", "))
	}
	t.Logf("checked %d methods: %s", len(called), strings.Join(called, ", "))
	if len(skipped) > 0 {
		t.Logf("returned before running gh: %s", strings.Join(skipped, ", "))
	}
}

// "" is still expressible, and still means the process's own directory. It is the
// right answer for a command the user typed in the repo they meant — which is why
// it is a value you pass rather than a default you inherit by saying nothing.
func TestAnEmptyDirectoryMeansTheProcessesOwn(t *testing.T) {
	rec := &dirRecorder{}
	_, _ = New(rec, "").ListPRs()
	if len(rec.dirs) != 1 || rec.dirs[0] != "" {
		t.Fatalf("expected one call with an empty dir, got %q", rec.dirs)
	}
}
