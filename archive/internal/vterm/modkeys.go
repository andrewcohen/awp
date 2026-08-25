package vterm

// Whether the hosted program has asked for xterm's modifyOtherKeys, tracked by
// reading its output.
//
// This is the one input mode awp has to work out for itself. Everything else the
// key encoder needs — application cursor keys, the keypad, the Kitty keyboard
// flags — comes back from ghostty_key_encoder_setopt_from_terminal, and asking
// the terminal is right because the terminal is what saw the request. For
// modifyOtherKeys that call answers `true` whether the program asked for state 2,
// asked for it to be turned off, or never mentioned it at all, and libghostty
// exposes no other way to read it. So shift+enter encoded as ESC[27;2;13~ into
// programs that had asked for nothing, and an escape sequence nobody asked for
// does not decorate the screen — it lands in the program's input buffer as
// garbage (#334).
//
// A sniffer rather than a carry buffer over each chunk: the request is a handful
// of bytes off a pty, so it can arrive split across two reads, and a state
// machine that survives a boundary is shorter than the bookkeeping to make a
// window that does. Pure Go and untagged so the default gate tests it — the
// emulator is only the thing being corrected, not part of the rule.

// modkeysSniffer is that state machine. Zero value is ready and means "nothing
// asked", which is a real terminal's starting state.
type modkeysSniffer struct {
	// state2 is what the program last asked for. Named for the option it feeds:
	// xterm has states 1 and 2, and libghostty's encoder only has the one flag,
	// for state 2. State 1 is the narrower behaviour, so it reads as off here
	// rather than as an approximation of the wider one.
	state2 bool

	st      sniffState
	private byte
	params  []int
	digits  bool // a digit has been seen since the last ';', so an omitted param is not a 0
}

type sniffState int

const (
	sniffGround sniffState = iota
	sniffEsc
	sniffCSI
)

// sniffMaxParams bounds the parameter list. A CSI sequence with more parameters
// than this is not one of the two this cares about, and a pty is untrusted input:
// without a cap, a program printing semicolons forever grows the slice forever.
const sniffMaxParams = 8

// feed reads a chunk of the program's output.
func (s *modkeysSniffer) feed(p []byte) {
	for _, b := range p {
		s.step(b)
	}
}

func (s *modkeysSniffer) step(b byte) {
	const esc = 0x1b
	switch s.st {
	case sniffGround:
		if b == esc {
			s.st = sniffEsc
		}
	case sniffEsc:
		switch b {
		case esc:
			// Stay: an ESC abandons whatever was being built and starts the next one.
		case '[':
			s.st, s.private, s.params, s.digits = sniffCSI, 0, s.params[:0], false
		case 'c':
			// RIS. A full reset puts the terminal back where it started, and this mode
			// is part of what it started without.
			s.state2 = false
			s.st = sniffGround
		default:
			s.st = sniffGround
		}
	case sniffCSI:
		s.csi(b)
	}
}

func (s *modkeysSniffer) csi(b byte) {
	switch {
	case b == 0x1b:
		s.st = sniffEsc
	case b >= '<' && b <= '?' && len(s.params) == 0 && !s.digits:
		// The private-marker slot, which is only a marker in the first position.
		s.private = b
	case b >= '0' && b <= '9':
		if len(s.params) == 0 || !s.digits {
			if len(s.params) >= sniffMaxParams {
				// Past the cap the parameter is dropped rather than the sequence: only
				// the first two decide anything, so a long tail is noise to skip, not a
				// reason to stop believing the start.
				s.digits = true
				return
			}
			s.params = append(s.params, 0)
		}
		s.digits = true
		s.params[len(s.params)-1] = s.params[len(s.params)-1]*10 + int(b-'0')
	case b == ';':
		if !s.digits && len(s.params) < sniffMaxParams {
			// An omitted parameter, which is its default rather than nothing at all.
			s.params = append(s.params, 0)
		}
		s.digits = false
	case b >= 0x40 && b <= 0x7e:
		s.final(b)
		s.st = sniffGround
	default:
		// An intermediate byte, or something that is not a CSI sequence at all. Either
		// way it is not one of the two forms below.
		s.st = sniffGround
	}
}

// final decides what a completed CSI sequence said.
//
// The form is `CSI > 4 ; Ps m` (XTMODKEYS). Ps omitted or 0 turns it off, which
// is why an omitted parameter has to reach here as a parameter rather than as an
// absence: `CSI > 4 m` is a reset, not a sequence to ignore.
func (s *modkeysSniffer) final(b byte) {
	if s.private != '>' || b != 'm' || len(s.params) == 0 || s.params[0] != 4 {
		return
	}
	s.state2 = len(s.params) > 1 && s.params[1] == 2
}
