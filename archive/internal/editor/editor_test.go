package editor

import (
	"reflect"
	"testing"
)

func TestBuildArgsCode(t *testing.T) {
	got := BuildArgs("code", "foo.go", 42)
	want := []string{"code", "--goto", "foo.go:42"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestBuildArgsVim(t *testing.T) {
	got := BuildArgs("nvim", "foo.go", 42)
	want := []string{"nvim", "+42", "foo.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestBuildArgsHelix(t *testing.T) {
	got := BuildArgs("hx", "foo.go", 42)
	want := []string{"hx", "foo.go:42"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestResolveFallsBack(t *testing.T) {
	t.Setenv("EDITOR", "")
	t.Setenv("VISUAL", "")
	if got := Resolve(); got != "vi" {
		t.Fatalf("got %q want vi", got)
	}
}
