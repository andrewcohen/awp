package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/workspace"
)

type fakeDoctor struct {
	runs int
	err  error
}

func (d *fakeDoctor) Run() error {
	d.runs++
	return d.err
}

type fakeService struct {
	openName     string
	openBookmark string
	openYes      bool
	renameOld    string
	renameNew    string
	deleteName   string
	deleteForce  bool
	listEntries  []workspace.ListEntry
	infoEntry    workspace.InfoEntry
	listErr      error
	infoErr      error
	openErr      error
	renameErr    error
	deleteErr    error
}

func (f *fakeService) List() ([]workspace.ListEntry, error) { return f.listEntries, f.listErr }
func (f *fakeService) Info(string) (workspace.InfoEntry, error) {
	return f.infoEntry, f.infoErr
}
func (f *fakeService) Open(name string, bookmark string, yes bool) error {
	f.openName = name
	f.openBookmark = bookmark
	f.openYes = yes
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

func TestRunDoctor(t *testing.T) {
	svc := &fakeService{}
	doc := &fakeDoctor{}
	app := NewApp(svc, &bytes.Buffer{})
	app.SetDoctor(doc)
	if err := app.Run([]string{"doctor"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if doc.runs != 1 {
		t.Fatalf("expected doctor to run once, got %d", doc.runs)
	}
}

func TestRunWorkspaceAlias(t *testing.T) {
	svc := &fakeService{listEntries: []workspace.ListEntry{{Name: "foo"}}}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"w", "list"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunDeleteParsesForceBeforeOrAfterName(t *testing.T) {
	tests := [][]string{{"workspace", "delete", "--force", "foo"}, {"workspace", "delete", "foo", "--force"}, {"workspace", "rm", "foo", "--force"}}
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
	if err := app.Run([]string{"w", "open", "-b", "team/example-branch"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.openName != "" || svc.openBookmark != "team/example-branch" || svc.openYes {
		t.Fatalf("unexpected open call: name=%q bookmark=%q yes=%t", svc.openName, svc.openBookmark, svc.openYes)
	}
}

func TestRunOpenParsesYesFlag(t *testing.T) {
	svc := &fakeService{}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"w", "open", "-y", "qa"}); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if svc.openName != "qa" || !svc.openYes {
		t.Fatalf("unexpected open call: name=%q yes=%t", svc.openName, svc.openYes)
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
	if err := app.Run([]string{"w", "rm", "--force"}); err != nil {
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
	svc := &fakeService{openErr: errors.New("boom")}
	app := NewApp(svc, &bytes.Buffer{})
	if err := app.Run([]string{"workspace", "open", "foo"}); err == nil {
		t.Fatal("expected error")
	}
}
