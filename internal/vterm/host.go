package vterm

import "image/color"

// HostColors is what the terminal awp itself is running in actually looks like.
//
// It exists because a hosted program asking its terminal a question is asking
// us, and some of those questions only the outer terminal can answer truthfully.
// OSC 10, 11 and 12 ask for the foreground, background and cursor colours; x/vt
// answers them out of its own defaults, which are white on black. So every pane
// told every program that it was running on a black background with white text,
// whatever was really behind it — and a program that picks a dim grey by
// blending toward the background was blending toward a background that was not
// there.
//
// A nil field means not known, and leaves x/vt's default in place. That is the
// honest state rather than an oversight: the terminal has to be asked, it answers
// asynchronously, and a pane opened in the first frames after boot has nothing
// yet to pass on. Guessing would put us back to inventing an answer, which is
// the thing being fixed.
type HostColors struct{ Fg, Bg, Cursor color.Color }
