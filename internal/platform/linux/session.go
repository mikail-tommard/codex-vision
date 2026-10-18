package linux

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

var sessionIDRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)
var displayRE = regexp.MustCompile(`^(?:unix)?:([0-9]+)(?:\.([0-9]+))?$`)

func localDisplay(value string) (string, error) {
	m := displayRE.FindStringSubmatch(value)
	if m == nil {
		return "", fmt.Errorf("local Unix DISPLAY required")
	}
	number, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		return "", fmt.Errorf("invalid DISPLAY number")
	}
	// logind normally reports the server (:0), not its default screen (:0.0).
	// Other X screens are not the same scope and are deliberately unsupported.
	if m[2] != "" && strings.TrimLeft(m[2], "0") != "" {
		return "", fmt.Errorf("non-default X screens are unsupported; use RandR monitors on DISPLAY :%d", number)
	}
	return fmt.Sprintf(":%d", number), nil
}

type SessionDiagnostics struct {
	ID            string `json:"session_id"`
	Display       string `json:"display"`
	LogindDisplay string `json:"logind_display"`
	Binding       string `json:"binding"`
	Seat          string `json:"seat"`
	Local         bool   `json:"local"`
	Active        bool   `json:"active"`
	Unlocked      bool   `json:"unlocked"`
}

func (d *Desktop) sessionState(ctx context.Context) (SessionDiagnostics, error) {
	r := SessionDiagnostics{Display: d.display}
	configured, err := localDisplay(d.display)
	if err != nil || os.Getenv("DISPLAY") != d.display {
		return r, fmt.Errorf("DISPLAY missing, invalid or changed since startup; restart from the intended local X11 session")
	}
	// 'self' is the actual caller session. Never use 'auto': it may select a
	// different graphical login for a caller running in SSH or a user service.
	b, err := d.run(ctx, "loginctl", "show-session", "self", "--all", "--no-pager", "-p", "Id", "-p", "User", "-p", "LockedHint", "-p", "Active", "-p", "Remote", "-p", "Type", "-p", "Display", "-p", "Seat")
	if err != nil {
		return r, fmt.Errorf("cannot verify caller's logind session; launch Codex/MCP from a terminal in the local X11 desktop: %w", err)
	}
	f := fields(b)
	r.ID, r.LogindDisplay, r.Seat = f["Id"], f["Display"], f["Seat"]
	r.Local, r.Active, r.Unlocked = f["Remote"] == "no", f["Active"] == "yes", f["LockedHint"] == "no"
	uid, err := strconv.Atoi(f["User"])
	if err != nil || uid != d.uid || !sessionIDRE.MatchString(r.ID) || (d.session != "" && d.session != r.ID) {
		return r, fmt.Errorf("caller logind session/UID does not match this process or XDG_SESSION_ID; refusing access")
	}
	if !r.Local || !r.Active || !r.Unlocked || f["Type"] != "x11" {
		return r, fmt.Errorf("logind must report the caller's local active unlocked X11 session; input and screen reads refused")
	}
	if r.LogindDisplay != "" {
		reported, err := localDisplay(r.LogindDisplay)
		if err != nil || reported != configured {
			return r, fmt.Errorf("logind display does not match DISPLAY; refusing access")
		}
		r.Binding = "logind_display"
		return r, nil
	}
	// Ubuntu/GDM can omit Display. Bind the caller's verified X11 session to
	// the active local seat instead; do not accept an arbitrary session ID.
	if !strings.HasPrefix(r.Seat, "seat") || !sessionIDRE.MatchString(r.Seat) {
		return r, fmt.Errorf("logind Display is empty and no local seat can be verified")
	}
	seat, err := d.run(ctx, "loginctl", "show-seat", r.Seat, "--all", "--no-pager", "-p", "ActiveSession")
	if err != nil || fields(seat)["ActiveSession"] != r.ID {
		return r, fmt.Errorf("logind Display is empty and the caller is not the seat's verified active session")
	}
	r.Binding = "caller_session_active_seat"
	return r, nil
}

func (d *Desktop) unlocked(ctx context.Context) error {
	_, err := d.sessionState(ctx)
	return err
}

// Diagnostics reads only logind state. It neither captures the screen nor sends input.
func (d *Desktop) Diagnostics(ctx context.Context) (any, error) {
	return d.sessionState(ctx)
}
