package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

func TestStdinChild(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-1] != "cv-stdin-child" {
		return
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	if string(data) == "diagnostic-case" {
		fmt.Fprintln(os.Stderr, "do-not-expose-user-text")
	}
	fmt.Print(string(data))
	os.Exit(0)
}

func TestInputRunnerStdinAndRedactedDiagnostics(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestStdinChild$", "--", "cv-stdin-child"}
	text := "Unicode Привет 🐧 --window 12; $(not-a-command)"
	out, err := (Native{}).RunInput(context.Background(), exe, text, args...)
	if err != nil || string(out) != text {
		t.Fatal("stdin delivery failed", err)
	}
	_, err = (Native{}).RunInput(context.Background(), exe, "diagnostic-case", args...)
	if err == nil || strings.Contains(err.Error(), "do-not-expose") {
		t.Fatal("diagnostic not redacted", err)
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
