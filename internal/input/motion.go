package input

import (
	"context"
	"fmt"
	"math"
	"time"
)

// MovePointer sends an interruptible path with an exact final coordinate.
// Each callback must validate the current window and OS input permissions.
func MovePointer(ctx context.Context, x, y, endX, endY int, duration time.Duration, post func(int, int) error) error {
	if duration < 10*time.Millisecond || duration > 5*time.Second {
		return fmt.Errorf("invalid cursor movement duration")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	steps := max(1, int(duration/(10*time.Millisecond)))
	ticker := time.NewTicker(duration / time.Duration(steps))
	defer ticker.Stop()
	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		t := float64(i) / float64(steps)
		eased := t * t * (3 - 2*t)
		px := x + int(math.Round(float64(endX-x)*eased))
		py := y + int(math.Round(float64(endY-y)*eased))
		if err := post(px, py); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// ApproachDuration controls only pointer approach, never the drag itself.
func ApproachDuration(mode, configured string) (time.Duration, error) {
	if mode == "" {
		mode = configured
	}
	if mode == "" {
		mode = "smooth"
	}
	switch mode {
	case "smooth":
		return 300 * time.Millisecond, nil
	case "rapid":
		return 20 * time.Millisecond, nil
	case "fast":
		return 70 * time.Millisecond, nil
	default:
		return 0, fmt.Errorf("motion must be rapid, fast or smooth")
	}
}
