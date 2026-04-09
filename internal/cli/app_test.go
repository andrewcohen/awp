package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

type fakeService struct {
	startName     string
	startBookmark string
	openName      string
	openBookmark  string
	renameOld     string
	renameNew     string
	deleteName    string
	deleteForce   bool
	listEntries   []workspace.ListEntry
	infoEntry     workspace.InfoEntry
	startErr      error
	listErr       error
	infoErr       error
	openErr       error
	renameErr     error
	deleteErr     error
}

func (f *fakeService) Start(name string, bookmark string) error {
	f.startName = name
	f.startBookmark = bookmark
	return f.startErr
}
func (f *fakeService) List() ([]workspace.ListEntry, error) { return f.listEntries, f.listErr }
func (f *fakeService) Info(string) (workspace.InfoEntry, error) {
	return f.infoEntry, f.infoErr
}
func (f *fakeService) Open(name string, bookmark string) error {
	f.openName = name
	f.openBookmark = bookmark
	return f.openErr
}
func (f *fakeService) Rename(oldName, newName string) error {
	f.renameOld, f.renameNew = oldName, newName
	return f.renameErr
}
func (f *fakeService) Delete(name string, force bool) error {
	f.deleteName, f.deleteForce = name, force
	return f.deleteErr
}

func TestRunStartParsesNameAndBookmarkFlags(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"workspace", "start", "--name", "foo", "-b", "my-bookmark"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.startName != "foo" || svc.startBookmark != "my-bookmark" {
		t.Fatalf("unexpected start args: name=%q bookmark=%q", svc.startName, svc.startBookmark)
	}
}

func TestRunWorkspaceAlias(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"w", "start", "foo"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.startName != "foo" {
		t.Fatalf("start name = %q, want foo", svc.startName)
	}
}

func TestRunDeleteParsesForceBeforeOrAfterName(t *testing.T) {
	tests := [][]string{{"workspace", "delete", "--force", "foo"}, {"workspace", "delete", "foo", "--force"}}
	for _, args := range tests {
		svc := &fakeService{}
		app := NewApp(svc, &bytes.Buffer{})
		if err := app.Run(args); err != nil {
			t.Fatalf("Run(%v) returned error: %v", args, err)
		}
		if !svc.deleteForce || svc.deleteName != "foo" {
			t.Fatalf("unexpected delete args for %v: force=%t name=%q", args, svc.deleteForce, svc.deleteName)
		}
	}
}

func TestRunListOutputsNamesOnly(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "foo", Active: true}, {Name: "bar", Active: false}}}
	out := &bytes.Buffer{}
	app := NewApp(svc, out)
	if err := app.Run([]string{"workspace", "list"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := out.String()
	if got != "foo\nbar\n" {
		t.Fatalf("unexpected list output: %q", got)
	}
}

func TestRunOpenHelp(t *testing.T) {
	svc := &fakeService{}
	out := &bytes.Buffer{}
	app := NewApp(svc, out)
	if err := app.Run([]string{"w", "open", "help"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(out.String(), "Usage: awp w open") {
		t.Fatalf("expected open usage, got %q", out.String())
	}
}

func TestRunOpenParsesBookmarkWithoutName(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"w", "open", "-b", "saltor/no-default-standard-delivery-preference"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.openName != "" || svc.openBookmark != "saltor/no-default-standard-delivery-preference" {
		t.Fatalf("unexpected open call: name=%q bookmark=%q", svc.openName, svc.openBookmark)
	}
}

func TestRunOpenUsesPickerWhenNoArg(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "qa"}, {Name: "default"}}}
	app := NewApp(svc, &bytes.Buffer{})
	app.in = bytes.NewBuffer(nil)
	app.picker = func(_ string, options []string) (string, error) {
		if len(options) != 2 {
			t.Fatalf("unexpected picker options: %#v", options)
		}
		return "qa", nil
	}
	if err := app.Run([]string{"w", "open"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.openName != "qa" {
		t.Fatalf("expected open qa, got %q", svc.openName)
	}
}

func TestRunDeleteUsesPickerWhenNoArg(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "qa"}}}
	app := NewApp(svc, &bytes.Buffer{})
	app.in = bytes.NewBuffer(nil)
	app.picker = func(_ string, _ []string) (string, error) { return "qa", nil }
	if err := app.Run([]string{"w", "remove", "--force"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.deleteName != "qa" || !svc.deleteForce {
		t.Fatalf("unexpected delete call: name=%q force=%t", svc.deleteName, svc.deleteForce)
	}
}

func TestRunInfoOutputsDetails(t *testing.T) {
	svc := &fakeService{infoEntry: workspace.InfoEntry{Name: "qa", Path: "/tmp/qa", Managed: true, JJExists: true, TmuxWindow: "qa", TmuxExists: true, ActiveWindow: true}}
	out := &bytes.Buffer{}
	app := NewApp(svc, out)
	if err := app.Run([]string{"workspace", "info", "qa"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	got := out.String()
	if !bytes.Contains([]byte(got), []byte("FIELD")) || !bytes.Contains([]byte(got), []byte("path")) {
		t.Fatalf("unexpected info output: %q", got)
	}
}

func TestRunPropagatesServiceError(t *testing.T) {
	svc := &fakeService{startErr: errors.New("boom")}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"workspace", "start", "foo"}); err == nil {
		t.Fatal("expected error")
	}
}
