package zmx

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// TestNoOperationAddressesAnUnnamedSession. Every one of these takes a name a
// caller computed, usually from SessionName over a deck Item's fields, and an
// Item arriving with a field missing is a thing this codebase has been bitten by
// more than once. An empty name is a bug upstream; the useful behaviour is to
// say so, not to hand a process manager an empty argument.
//
// Kill is the one that mattered enough to write this: it had no guard, and
// `zmx kill "" --force` is the shape of an accident that has already cost a live
// session here.
func TestNoOperationAddressesAnUnnamedSession(t *testing.T) {
	for _, name := range []string{"", "   ", "\t"} {
		var ran [][]string
		c := New(func(_ context.Context, _ string, cmd string, args ...string) (string, error) {
			ran = append(ran, append([]string{cmd}, args...))
			return "", nil
		})
		ctx := context.Background()

		for _, op := range []struct {
			what string
			call func() error
		}{
			{"Kill", func() error { return c.Kill(ctx, name) }},
			{"Reap", func() error { _, err := c.Reap(ctx, name); return err }},
			{"Paste", func() error { return c.Paste(ctx, name, "hello") }},
			{"Label", func() error { return c.Label(ctx, name, map[string]string{"k": "v"}) }},
			{"Lookup", func() error { _, _, err := c.Lookup(ctx, name); return err }},
			{"History", func() error { _, err := c.History(ctx, name); return err }},
		} {
			err := op.call()
			if err == nil {
				t.Errorf("%s(%q) succeeded", op.what, name)
				continue
			}
			if !strings.Contains(err.Error(), "no name given") {
				t.Errorf("%s(%q) failed with %q, want it to say no name was given", op.what, name, err)
			}
		}
		if len(ran) != 0 {
			t.Errorf("with name %q, zmx was still run: %v", name, ran)
		}
	}
}

// TestEveryNameTakingMethodIsGuarded catches the next method added without one.
// The guard is only as strong as every call site remembering, which is the kind
// of invariant this repo pins by reflection rather than by review.
func TestEveryNameTakingMethodIsGuarded(t *testing.T) {
	// List takes no name, so it is the one exemption and is named as such.
	exempt := map[string]bool{"List": true}

	typ := reflect.TypeOf(Client{})
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()
	checked := 0
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if exempt[m.Name] {
			continue
		}
		// Shape we care about: (ctx, name string, ...). Anything else is not a
		// per-session operation.
		if m.Type.NumIn() < 3 || m.Type.In(1) != ctxType || m.Type.In(2).Kind() != reflect.String {
			continue
		}
		checked++

		var ran bool
		c := New(func(_ context.Context, _ string, _ string, _ ...string) (string, error) {
			ran = true
			return "", nil
		})
		args := []reflect.Value{reflect.ValueOf(c), reflect.ValueOf(context.Background()), reflect.ValueOf("")}
		for j := 3; j < m.Type.NumIn(); j++ {
			args = append(args, reflect.Zero(m.Type.In(j)))
		}
		m.Func.Call(args)
		if ran {
			t.Errorf("%s ran zmx with an empty session name — add the named() guard", m.Name)
		}
	}
	if checked == 0 {
		t.Fatal("walked no per-session methods; this guard is measuring nothing")
	}
}
