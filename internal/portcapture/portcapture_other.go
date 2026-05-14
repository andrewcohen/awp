//go:build !darwin && !linux

package portcapture

import "context"

// listListeners is a no-op on unsupported OSes. The feature gracefully
// degrades to "no dev URL ever discovered" instead of erroring.
func listListeners(_ context.Context) ([]Listener, error) {
	return nil, nil
}
