//go:build ghosttyvt

package vterm

// The exported half of the reply path. It is its own file because a cgo file
// containing //export must not define anything in its preamble, and ghostty.go's
// preamble defines the trampoline that calls this.

/*
#include <stdint.h>
#include <stddef.h>
*/
import "C"

import "unsafe"

// awpGhosttyWritePty carries the terminal's answer to a device query back to the
// program that asked it.
//
// Keyed on the terminal handle, which the callback is handed, rather than on
// anything of ours: cgo forbids giving C a Go pointer it holds onto, and an
// integer id smuggled through userdata means converting a uintptr back to an
// unsafe.Pointer — a misuse go vet flags whether or not the value was a pointer.
//
// A handle with no terminal is not an error. The process can exit between asking
// a question and the answer being written.
//
//export awpGhosttyWritePty
func awpGhosttyWritePty(terminal unsafe.Pointer, data *C.uint8_t, n C.size_t) {
	if n == 0 {
		return
	}
	hosts.Lock()
	t := hosts.byTerm[uintptr(terminal)]
	hosts.Unlock()
	if t == nil || t.in == nil {
		return
	}
	// The bytes are only valid for the duration of the call, so they are copied
	// before the write rather than after.
	_, _ = t.in.Write(C.GoBytes(unsafe.Pointer(data), C.int(n)))
}
