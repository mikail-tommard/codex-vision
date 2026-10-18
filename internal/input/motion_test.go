package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPointerPathEndsExactlyAndStaysWithinEndpoints(t *testing.T) {
	prevX, prevY := -20, 90
	calls := 0
	err := MovePointer(context.Background(), prevX, prevY, 80, -30, 40*time.Millisecond, func(x, y int) error {
		if x < prevX || x > 80 || y > prevY || y < -30 {
			t.Fatalf("invalid pointer path: %d,%d after %d,%d", x, y, prevX, prevY)
		}
		prevX, prevY = x, y
		calls++
		return nil
	})
	if err != nil || calls < 2 || prevX != 80 || prevY != -30 {
		t.Fatal(err, calls, prevX, prevY)
	}
}
func TestPointerPathStopsBeforeAnotherEvent(t *testing.T) {
	for _, kind := range []string{"cancel", "callback_error", "already_cancelled", "invalid_duration"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			duration := 40 * time.Millisecond
			failure := errors.New("window changed")
			if kind == "already_cancelled" {
				cancel()
			}
			if kind == "invalid_duration" {
				duration = -time.Second
			}
			err := MovePointer(ctx, 0, 0, 200, 100, duration, func(x, y int) error {
				calls++
				if kind == "callback_error" {
					return failure
				}
				cancel()
				return nil
			})
			if err == nil || calls > 1 {
				t.Fatal(err, calls)
			}
			if (kind == "already_cancelled" || kind == "invalid_duration") && calls != 0 {
				t.Fatal("input after preflight failure")
			}
			if kind == "callback_error" && !errors.Is(err, failure) {
				t.Fatal(err)
			}
		})
	}
}

func TestApproachModes(t *testing.T) {
	for _, tc := range []struct {
		mode, configured string
		want             time.Duration
	}{{"", "", 300 * time.Millisecond}, {"", "rapid", 20 * time.Millisecond}, {"rapid", "smooth", 20 * time.Millisecond}, {"", "fast", 70 * time.Millisecond}, {"smooth", "fast", 300 * time.Millisecond}, {"fast", "smooth", 70 * time.Millisecond}} {
		got, err := ApproachDuration(tc.mode, tc.configured)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	if _, err := ApproachDuration("instant", "smooth"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}
