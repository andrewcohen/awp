package cli

import (
	"path/filepath"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fsnotify/fsnotify"

	"github.com/andrewcohen/awp/internal/deckui"
	"github.com/andrewcohen/awp/internal/state"
)

const deckStateWatchDebounce = 75 * time.Millisecond

// newDeckStateChangeWatcher watches the global workspace-state.json parent
// directory and emits deckui.StateChangedMsg when the state file is created,
// replaced, renamed, or written. It is best-effort: setup failures and watcher
// errors are silent because the deck still has its periodic polling fallback.
func newDeckStateChangeWatcher() deckui.StateChangeWatcher {
	path, err := state.GlobalStorePath()
	if err != nil {
		return nil
	}
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	changes := make(chan struct{}, 1)
	done := make(chan struct{})
	var once sync.Once
	active := false

	start := func() {
		watcher, err := fsnotify.NewWatcher()
		if err != nil {
			return
		}
		if err := watcher.Add(dir); err != nil {
			_ = watcher.Close()
			return
		}
		active = true
		go func() {
			defer watcher.Close()
			defer close(done)
			var debounce <-chan time.Time
			pending := false
			for {
				select {
				case event, ok := <-watcher.Events:
					if !ok {
						return
					}
					if filepath.Base(event.Name) != base || !isStateChangeOp(event.Op) {
						continue
					}
					if !pending {
						pending = true
						debounce = time.After(deckStateWatchDebounce)
					}
				case _, ok := <-watcher.Errors:
					if !ok {
						return
					}
					// Ignore individual watcher errors; polling remains authoritative.
				case <-debounce:
					pending = false
					debounce = nil
					select {
					case changes <- struct{}{}:
					default:
					}
				}
			}
		}()
	}

	return func() tea.Cmd {
		once.Do(start)
		if !active {
			return nil
		}
		return func() tea.Msg {
			select {
			case <-changes:
				return deckui.StateChangedMsg{}
			case <-done:
				return nil
			}
		}
	}
}

func isStateChangeOp(op fsnotify.Op) bool {
	return op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0
}
