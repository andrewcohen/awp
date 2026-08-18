package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Probe is how the frontend reports a POC result back to something that can be
// read without a human looking at the window.
//
// The questions this surface exists to answer are mostly answered in the
// webview — wasm instantiates or it does not, a pane keeps up or it does not —
// and the obvious way to check is to look at the screen. That does not survive
// being run twice: nobody diffs two screenshots, and a result nobody recorded is
// a result that gets remembered as better than it was. So each step reports
// through here and the answer lands in gdeck's log next to the timings.
//
// It is not a substitute for looking. Whether a pane *feels* right is exactly
// the thing a pass/fail line cannot carry, which is why latency is reported as a
// number rather than a verdict.
type Probe struct{}

// Report records the outcome of one POC check. detail carries the error when ok
// is false, and whatever measurement the check produced when it is true.
func (p *Probe) Report(check string, ok bool, detail string) {
	level := slog.LevelInfo
	result := "pass"
	if !ok {
		level, result = slog.LevelError, "FAIL"
	}
	slog.Log(context.Background(), level, "gdeck probe",
		"check", check, "result", result, "detail", detail)
}

// shotName is what a snapshot is allowed to be called: the check's own name, and
// nothing that could climb out of the directory it is written to.
var shotName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

const pngDataURL = "data:image/png;base64,"

// Snapshot writes a PNG the frontend produced of its own canvas and returns the
// path, so a layout can be checked without photographing the developer's screen.
//
// A screen capture is the obvious way to see what a window looks like and the
// wrong one: the frame contains whatever else was on the desktop — mail, chat,
// another project's terminal — and none of that is the app's to collect. What is
// actually being checked here is a canvas the pane drew, and a canvas can export
// itself. So the image comes out of toDataURL and arrives here, bounded to
// exactly the pixels gdeck rendered.
//
// The name is validated rather than trusted. Nothing hostile is expected from
// gdeck's own frontend, but this method turns a string from the webview into a
// filesystem path, and that is the shape of a path traversal whoever writes the
// next caller should not have to notice.
func (p *Probe) Snapshot(check, dataURL string) (string, error) {
	if !shotName.MatchString(check) {
		return "", fmt.Errorf("probe snapshot: name %q is not a lowercase kebab-case check name", check)
	}
	payload, ok := strings.CutPrefix(dataURL, pngDataURL)
	if !ok {
		return "", fmt.Errorf("probe snapshot %s: want a %s data URL, got %d bytes starting %.32q",
			check, pngDataURL, len(dataURL), dataURL)
	}
	png, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("probe snapshot %s: decoding the data URL's base64: %w", check, err)
	}

	dir := filepath.Join(os.TempDir(), "gdeck-shots")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("probe snapshot %s: creating %s: %w", check, dir, err)
	}
	path := filepath.Join(dir, check+".png")
	if err := os.WriteFile(path, png, 0o600); err != nil {
		return "", fmt.Errorf("probe snapshot %s: writing %s: %w", check, path, err)
	}

	slog.Info("gdeck probe snapshot", "check", check, "bytes", len(png), "path", path)
	return path, nil
}
