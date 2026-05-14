package portcapture

import (
	"reflect"
	"sort"
	"testing"
)

func TestParsePsOutput(t *testing.T) {
	in := `    1     0
  100     1
  200   100
  300   100
  301   300
notapid blah
  400
`
	got := parsePsOutput(in)
	want := map[int]int{
		1:   0,
		100: 1,
		200: 100,
		300: 100,
		301: 300,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePsOutput: got %v want %v", got, want)
	}
}

func TestDescendants(t *testing.T) {
	parents := map[int]int{
		100: 1,
		200: 100,
		300: 100,
		301: 300,
	}
	children := invertParents(parents)
	got := descendants(100, children)
	sort.Ints(got)
	want := []int{100, 200, 300, 301}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("descendants: got %v want %v", got, want)
	}
}

func TestParseLsofOutput(t *testing.T) {
	in := `p12345
n*:5173
n127.0.0.1:24678
p12346
n[::1]:3000
p12347
n[::]:8080
`
	got := parseLsofOutput(in)
	want := []Listener{
		{PID: 12345, Port: 5173, Addr: "*"},
		{PID: 12345, Port: 24678, Addr: "127.0.0.1"},
		{PID: 12346, Port: 3000, Addr: "::1"},
		{PID: 12347, Port: 8080, Addr: "::"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseLsofOutput:\n got %#v\nwant %#v", got, want)
	}
}

func TestParseLsofOutputSkipsPeerLines(t *testing.T) {
	// `n` lines containing "->" are connection peers, not bind addrs.
	// We're filtering on LISTEN so these shouldn't appear, but guard.
	in := `p12345
n127.0.0.1:5173->10.0.0.1:443
n*:5173
`
	got := parseLsofOutput(in)
	want := []Listener{{PID: 12345, Port: 5173, Addr: "*"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseLsofOutput peer-skip:\n got %#v\nwant %#v", got, want)
	}
}

func TestParseSsOutput(t *testing.T) {
	in := `LISTEN 0      128       127.0.0.1:5173       0.0.0.0:*    users:(("vite",pid=12345,fd=20))
LISTEN 0      128               *:3000             *:*    users:(("node",pid=12346,fd=18))
LISTEN 0      128         [::1]:8080            [::]:*    users:(("api",pid=12347,fd=10))
LISTEN 0      128         [::]:9090             [::]:*    users:(("shared",pid=12348,fd=4),("shared",pid=12349,fd=4))
`
	got := parseSsOutput(in)
	want := []Listener{
		{PID: 12345, Port: 5173, Addr: "127.0.0.1"},
		{PID: 12346, Port: 3000, Addr: "0.0.0.0"},
		{PID: 12347, Port: 8080, Addr: "::1"},
		{PID: 12348, Port: 9090, Addr: "::"},
		{PID: 12349, Port: 9090, Addr: "::"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSsOutput:\n got %#v\nwant %#v", got, want)
	}
}

func TestPickURL(t *testing.T) {
	cases := []struct {
		name string
		in   []Listener
		want string
	}{
		{
			name: "empty",
			in:   nil,
			want: "",
		},
		{
			name: "single",
			in:   []Listener{{PID: 1, Port: 5173, Addr: "127.0.0.1"}},
			want: "http://localhost:5173",
		},
		{
			name: "vite + hmr — lowest wins",
			in: []Listener{
				{PID: 1, Port: 5173, Addr: "0.0.0.0"},
				{PID: 1, Port: 24678, Addr: "127.0.0.1"},
			},
			want: "http://localhost:5173",
		},
		{
			name: "close ports — loopback wins on tiebreak",
			in: []Listener{
				{PID: 1, Port: 5174, Addr: "0.0.0.0"},
				{PID: 1, Port: 5173, Addr: "127.0.0.1"},
			},
			want: "http://localhost:5173",
		},
		{
			name: "close ports — wildcard loses to loopback within 100",
			in: []Listener{
				{PID: 1, Port: 3000, Addr: "0.0.0.0"},
				{PID: 1, Port: 3050, Addr: "127.0.0.1"},
			},
			want: "http://localhost:3050",
		},
		{
			name: "far apart — lowest wins regardless of bind",
			in: []Listener{
				{PID: 1, Port: 3000, Addr: "0.0.0.0"},
				{PID: 1, Port: 9000, Addr: "127.0.0.1"},
			},
			want: "http://localhost:3000",
		},
		{
			name: "ephemeral-only — nothing surfaces (Claude Code at 59128)",
			in:   []Listener{{PID: 1, Port: 59128, Addr: "127.0.0.1"}},
			want: "",
		},
		{
			name: "ephemeral plus real — real wins",
			in: []Listener{
				{PID: 1, Port: 59128, Addr: "127.0.0.1"},
				{PID: 2, Port: 5173, Addr: "127.0.0.1"},
			},
			want: "http://localhost:5173",
		},
		{
			name: "above ceiling — dropped (legacy Expo on 19000)",
			in:   []Listener{{PID: 1, Port: 19000, Addr: "127.0.0.1"}},
			want: "",
		},
		{
			name: "edge of range — accepted",
			in: []Listener{
				{PID: 1, Port: 1024, Addr: "127.0.0.1"},
				{PID: 2, Port: 9999, Addr: "127.0.0.1"},
			},
			want: "http://localhost:1024",
		},
		{
			name: "below floor — dropped",
			in:   []Listener{{PID: 1, Port: 80, Addr: "127.0.0.1"}},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickURL(c.in); got != c.want {
				t.Fatalf("pickURL(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSplitAddrPort(t *testing.T) {
	cases := []struct {
		in       string
		addr     string
		port     int
		ok       bool
	}{
		{in: "*:5173", addr: "*", port: 5173, ok: true},
		{in: "127.0.0.1:5173", addr: "127.0.0.1", port: 5173, ok: true},
		{in: "[::1]:5173", addr: "::1", port: 5173, ok: true},
		{in: "[::]:8080", addr: "::", port: 8080, ok: true},
		{in: "garbage", ok: false},
		{in: "[::1]", ok: false},
		{in: "127.0.0.1:abc", ok: false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			addr, port, ok := splitAddrPort(c.in)
			if ok != c.ok || addr != c.addr || port != c.port {
				t.Fatalf("splitAddrPort(%q) = (%q, %d, %v), want (%q, %d, %v)",
					c.in, addr, port, ok, c.addr, c.port, c.ok)
			}
		})
	}
}
