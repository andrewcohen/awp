package vterm

import "testing"

// The sniffer for #334. No build tag: what a request looks like on the wire is a
// property of xterm's protocol, not of the emulator behind the pane, so this runs
// on the default gate where the tagged pane test does not.

// TestNothingAskedIsNothingOn is the case the bug was. A program that has said
// nothing must leave the mode off, because the encoder is going to be told this
// answer and a `true` here is an escape sequence in that program's input.
func TestNothingAskedIsNothingOn(t *testing.T) {
	var s modkeysSniffer
	s.feed([]byte("hello\r\n\x1b[?25l\x1b[2J\x1b[1;1H"))
	if s.state2 {
		t.Error("a program that never mentioned modifyOtherKeys turned it on")
	}
}

// TestTheRequestIsHeard, and its off switches. `CSI > 4 m` with no parameter is a
// reset rather than a sequence to skip, which is why an omitted parameter has to
// survive as far as the decision.
func TestTheRequestIsHeard(t *testing.T) {
	for _, tc := range []struct {
		seq  string
		want bool
	}{
		{"\x1b[>4;2m", true},
		{"\x1b[>4;1m", false}, // state 1 is the narrower behaviour, and not this flag
		{"\x1b[>4;0m", false},
		{"\x1b[>4m", false},
		{"\x1b[>4;2m\x1b[>4;0m", false},
		{"\x1b[>4;0m\x1b[>4;2m", true},
		{"\x1b[>4;2m\x1bc", false}, // RIS puts the terminal back where it started
		{"\x1b[4;2m", false},       // no private marker: an SGR sequence, not this
		{"\x1b[>4;2H", false},      // not the final byte this mode uses
		{"\x1b[>5;2m", false},      // a different XTMODKEYS resource
		{"\x1b[>4;2;9;9;9;9;9;9;9m", true},
	} {
		var s modkeysSniffer
		s.feed([]byte(tc.seq))
		if s.state2 != tc.want {
			t.Errorf("%q left the mode %v, want %v", tc.seq, s.state2, tc.want)
		}
	}
}

// TestARequestSplitAcrossReadsStillArrives, which is the whole reason this is a
// state machine. A pty hands over whatever has been written so far, so the seven
// bytes of a request are under no obligation to arrive together.
func TestARequestSplitAcrossReadsStillArrives(t *testing.T) {
	const seq = "\x1b[>4;2m"
	for cut := 1; cut < len(seq); cut++ {
		var s modkeysSniffer
		s.feed([]byte(seq[:cut]))
		s.feed([]byte(seq[cut:]))
		if !s.state2 {
			t.Errorf("split after %d bytes lost the request", cut)
		}
	}
}

// TestAnAbandonedSequenceDoesNotLeak. An ESC mid-sequence starts the next one, so
// a request interrupted by a real one must not be answered from the wreckage of
// both.
func TestAnAbandonedSequenceDoesNotLeak(t *testing.T) {
	var s modkeysSniffer
	s.feed([]byte("\x1b[>4;\x1b[>4;2m"))
	if !s.state2 {
		t.Error("the request that followed an abandoned one was lost")
	}
	s.feed([]byte("\x1b[>4\x1b[>4;0m"))
	if s.state2 {
		t.Error("the reset that followed an abandoned sequence was lost")
	}
}

// TestAFloodOfParametersIsBounded. The pty is untrusted input: a program printing
// semicolons must not grow the parameter list for as long as it keeps printing.
func TestAFloodOfParametersIsBounded(t *testing.T) {
	var s modkeysSniffer
	flood := make([]byte, 0, 4096)
	flood = append(flood, "\x1b[>"...)
	for range 4000 {
		flood = append(flood, '1', ';')
	}
	s.feed(flood)
	if len(s.params) > sniffMaxParams {
		t.Errorf("the parameter list grew to %d, past the %d cap", len(s.params), sniffMaxParams)
	}
	// And the sniffer still works afterwards.
	s.feed([]byte("m\x1b[>4;2m"))
	if !s.state2 {
		t.Error("the sniffer did not recover from a flood")
	}
}
