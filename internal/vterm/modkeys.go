package vterm

import (
	"io"
	"strconv"
	"strings"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
)

// A legacy terminal cannot say "shift+enter". Enter is CR, and CR carries no
// modifiers, so shift+enter, ctrl+enter and enter are the same three bytes —
// which is why agents that bind shift+enter to "newline, don't submit" first ask
// the terminal for an encoding that can express it. Two ways to ask are in use:
//
//	CSI > <flags> u     the Kitty keyboard protocol, flags 0 turns it back off
//	CSI > 4 ; <n> m     xterm's modifyOtherKeys, n of 1 or 2 on and 0 off
//
// Claude Code sends both, in that order, and pops them on exit. Whichever it got
// decides what shift+enter has to look like, so a pane that guesses is a pane
// where either the key does nothing or every enter arrives as an escape
// sequence.
//
// The requests have to be read out of the pane's own output because x/vt neither
// answers them nor reports them — its key encoder has a standing TODO for CSI u
// and modifyOtherKeys — so there is no callback to hang this on.
type keyEncoding int

const (
	// encodingLegacy is a bare CR: nothing asked for anything better, so
	// shift+enter is indistinguishable from enter and is sent as one.
	encodingLegacy keyEncoding = iota
	encodingModifyOtherKeys
	encodingKitty
)

// keyRequests is what a pane's program has asked for, as an atomic because it is
// written by the goroutine copying output into the emulator and read by whichever
// one delivers a keypress.
type keyRequests struct{ enc atomic.Int32 }

func (k *keyRequests) encoding() keyEncoding { return keyEncoding(k.enc.Load()) }

// modeSniffer watches a pane's output for those requests on the way to the
// emulator, and passes every byte through untouched.
type modeSniffer struct {
	next io.Writer
	keys *keyRequests
	// carry is the tail of the previous chunk when it ended mid-sequence. A
	// request can be split across writes — it arrives in whatever chunks the pty
	// hands over — and a scan without this misses one whenever the split lands
	// inside it, which reads as the protocol working sometimes.
	carry []byte
}

// maxCarry bounds what an unfinished sequence may hold. Anything longer than a
// handful of parameter digits is not one of the two requests, and an unbounded
// carry would grow for the life of a pane on a program that writes a lone ESC [.
const maxCarry = 32

func (s *modeSniffer) Write(p []byte) (int, error) {
	s.scan(p)
	// The count must describe p, not the buffer we scanned: io.Copy checks it
	// against what it handed over.
	n, err := s.next.Write(p)
	return n, err
}

func (s *modeSniffer) scan(p []byte) {
	buf := p
	if len(s.carry) > 0 {
		// A fresh slice rather than appending onto carry: the result is scanned
		// and re-sliced, and reusing carry's array would have it aliasing what it
		// is about to be reassigned from.
		joined := make([]byte, 0, len(s.carry)+len(p))
		joined = append(joined, s.carry...)
		joined = append(joined, p...)
		buf = joined
	}
	s.carry = nil

	for {
		i := strings.Index(string(buf), "\x1b[>")
		if i < 0 {
			// The introducer itself can be split — a chunk ending in ESC or
			// ESC [ is the start of the next one.
			for _, partial := range []string{"\x1b[", "\x1b"} {
				if strings.HasSuffix(string(buf), partial) {
					s.carry = []byte(partial)
					return
				}
			}
			return
		}
		rest := buf[i+3:]
		end := -1
		for j, b := range rest {
			if b >= '0' && b <= '9' || b == ';' {
				continue
			}
			end = j
			break
		}
		if end < 0 {
			// Ends mid-sequence: hold it for the next chunk, unless it is too
			// long to be one of the requests.
			if len(rest) <= maxCarry {
				s.carry = append([]byte(nil), buf[i:]...)
			}
			return
		}
		s.apply(string(rest[:end]), rest[end])
		buf = rest[end+1:]
	}
}

// apply records one CSI > … request. Anything that is not one of the two is
// ignored: a pane must not care about the rest of the private-mode space.
func (s *modeSniffer) apply(params string, final byte) {
	fields := strings.Split(params, ";")
	switch final {
	case 'u':
		// CSI > flags u. Flags of 0 is the documented way to ask for nothing,
		// and is what a program sends when it is done.
		if flags, err := strconv.Atoi(first(fields)); err == nil && flags != 0 {
			s.keys.enc.Store(int32(encodingKitty))
		} else if err == nil {
			s.keys.enc.CompareAndSwap(int32(encodingKitty), int32(encodingLegacy))
		}
	case 'm':
		// CSI > 4 ; n m. A bare CSI > 4 m means "back to the default", which is
		// off, so an absent second parameter is a 0.
		if first(fields) != "4" {
			return
		}
		n := 0
		if len(fields) > 1 {
			n, _ = strconv.Atoi(fields[1])
		}
		if n > 0 {
			// Kitty is the better protocol and a program asking for both means
			// it prefers that one, so this must not overwrite it.
			s.keys.enc.CompareAndSwap(int32(encodingLegacy), int32(encodingModifyOtherKeys))
			return
		}
		s.keys.enc.CompareAndSwap(int32(encodingModifyOtherKeys), int32(encodingLegacy))
	}
}

func first(fields []string) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// enterKeyBytes is what to send for enter with mods held, given what the pane's
// program asked to be told.
//
// Returns "" when the modifiers are ones the encodings do not carry, leaving the
// caller's ordinary path to handle it.
func enterKeyBytes(mod tea.KeyMod, enc keyEncoding) string {
	// Kitty numbers modifiers from 1, adding a bit per modifier held. xterm's
	// modifyOtherKeys uses the same numbering, which is why one calculation
	// serves both.
	param := 1
	for _, m := range []struct {
		mod tea.KeyMod
		bit int
	}{
		{tea.ModShift, 1},
		{tea.ModAlt, 2},
		{tea.ModCtrl, 4},
		{tea.ModSuper, 8},
	} {
		if mod&m.mod != 0 {
			param += m.bit
		}
	}
	if param == 1 {
		return "" // no modifiers: plain enter, which is a CR by every encoding
	}
	switch enc {
	case encodingKitty:
		return "\x1b[13;" + strconv.Itoa(param) + "u"
	case encodingModifyOtherKeys:
		return "\x1b[27;" + strconv.Itoa(param) + ";13~"
	case encodingLegacy:
		// Nothing asked for an encoding that can say this, so the honest answer
		// is what a real terminal would send: a CR. Inventing an escape sequence
		// here would put one in a program's input buffer.
		return "\r"
	}
	return "\r"
}
