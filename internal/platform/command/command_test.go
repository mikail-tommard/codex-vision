package command

import (
	"context"
	"errors"
	"testing"
)

func TestBoundedOutput(t *testing.T) {
	w := &bounded{limit: 4}
	if _, e := w.Write([]byte("test")); e != nil {
		t.Fatal(e)
	}
	if _, e := w.Write([]byte("more")); e == nil || w.Len() != 4 {
		t.Fatal("output bound not enforced")
	}
}
func TestCancelledBeforeSpawn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := (Native{}).Run(ctx, "must-not-execute")
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
