package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andrewcohen/awp/internal/awplog"
)

// `awp logs` is the other half of the log existing: a file nobody can find is not
// much better than no file.
func TestLogsPrintsThePathAndTheTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)
	awplog.Errorf("first")
	awplog.Errorf("second")

	var out bytes.Buffer
	if err := runLogs(nil, &out); err != nil {
		t.Fatalf("logs: %v", err)
	}
	got := out.String()
	// The path leads, so a pasted answer says where it came from.
	if !strings.HasPrefix(got, path) {
		t.Fatalf("expected the path first, got %q", got)
	}
	if !strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("expected both entries, got %q", got)
	}
}

func TestLogsRespectsTheLineCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)
	for i := 0; i < 10; i++ {
		awplog.Infof("entry %d", i)
	}
	var out bytes.Buffer
	if err := runLogs([]string{"-n", "2"}, &out); err != nil {
		t.Fatalf("logs: %v", err)
	}
	// Two entries plus the path line.
	if n := len(strings.Split(strings.TrimRight(out.String(), "\n"), "\n")); n != 3 {
		t.Fatalf("expected 2 entries and the path, got %d lines:\n%s", n, out.String())
	}
	if !strings.Contains(out.String(), "entry 9") {
		t.Fatalf("expected the most recent entries, got %q", out.String())
	}
}

// --path is for `tail -f $(awp logs --path)`, which is what you want while
// reproducing something rather than afterwards.
func TestLogsPathOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)
	var out bytes.Buffer
	if err := runLogs([]string{"--path"}, &out); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if strings.TrimSpace(out.String()) != path {
		t.Fatalf("expected just the path, got %q", out.String())
	}
}

// An empty log means nothing has gone wrong yet. Saying so beats an error about a
// missing file that is missing for a good reason.
func TestLogsOnAnEmptyLogSaysSoWithoutFailing(t *testing.T) {
	awplog.SetPathForTest(t, filepath.Join(t.TempDir(), "never-written.log"))
	var out bytes.Buffer
	if err := runLogs(nil, &out); err != nil {
		t.Fatalf("expected no error for an unwritten log, got %v", err)
	}
	if !strings.Contains(out.String(), "nothing logged yet") {
		t.Fatalf("expected the empty case explained, got %q", out.String())
	}
}

// The command is reachable, which is the whole point of adding it.
func TestLogsIsWiredIntoTheCommandTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "awp.log")
	awplog.SetPathForTest(t, path)
	awplog.Errorf("reachable")
	var out bytes.Buffer
	app := NewApp(nil, &out)
	if err := app.Run([]string{"logs"}); err != nil {
		t.Fatalf("awp logs: %v", err)
	}
	if !strings.Contains(out.String(), "reachable") {
		t.Fatalf("expected the log's contents, got %q", out.String())
	}
	// And named in the usage line, since a command nobody is told about is one
	// nobody uses at the moment they need it.
	var usage bytes.Buffer
	other := NewApp(nil, &usage)
	_ = other.Run(nil)
	if !strings.Contains(usage.String(), "logs") {
		t.Fatalf("expected logs in the usage line, got %q", usage.String())
	}
	_ = os.Remove(path)
}
