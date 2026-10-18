package linux

import (
	"context"
	"strings"
	"testing"
)

func TestLogindDisplayBinding(t *testing.T) {
	base := "Id=c1\nUser=1000\nSeat=seat0\nActive=yes\nRemote=no\nType=x11\nLockedHint=no\nDisplay=:0\n"
	for _, tc := range []struct {
		name, from, to, seat string
		want                 bool
	}{
		{"reported", "", "", "", true},
		{"screen_zero", "Display=:0", "Display=unix:0.0", "", true},
		{"empty_display", "Display=:0", "Display=", "", true},
		{"wrong_seat_session", "Display=:0", "Display=", "c2", false},
		{"no_seat", "Display=:0\n", "Display=\nSeat=\n", "", false},
		{"wrong_display", "Display=:0", "Display=:1", "", false},
		{"wrong_screen", "Display=:0", "Display=:0.1", "", false},
		{"other_uid", "User=1000", "User=1001", "", false},
		{"missing_uid", "User=1000", "User=", "", false},
		{"other_session", "Id=c1", "Id=c2", "", false},
		{"locked", "LockedHint=no", "LockedHint=yes", "", false},
		{"inactive", "Active=yes", "Active=no", "", false},
		{"remote", "Remote=no", "Remote=yes", "", false},
		{"wayland", "Type=x11", "Type=wayland", "", false},
		{"ssh", "Type=x11", "Type=tty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, f := testDesktop(t)
			f.sessionFields = strings.Replace(base, tc.from, tc.to, 1)
			f.seatSession = tc.seat
			report, err := d.sessionState(context.Background())
			if (err == nil) != tc.want {
				t.Fatalf("report=%+v err=%v", report, err)
			}
			for _, c := range f.calls {
				if strings.Contains(c, "show-session") && !strings.Contains(c, "show-session self ") {
					t.Fatal("not bound to real caller", c)
				}
			}
			if tc.name == "empty_display" && report.Binding != "caller_session_active_seat" {
				t.Fatal(report)
			}
		})
	}
}

func TestLogindResolvesMissingSessionEnvironment(t *testing.T) {
	d, _ := testDesktop(t)
	d.session = ""
	if _, err := d.sessionState(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISPLAY", ":1")
	if _, err := d.sessionState(context.Background()); err == nil {
		t.Fatal("changed DISPLAY accepted")
	}
}

func TestLocalDisplayCanonicalization(t *testing.T) {
	for _, value := range []string{":0", ":0.0", "unix:0", "unix:0.0"} {
		if result, err := localDisplay(value); err != nil || result != ":0" {
			t.Fatal(value, result, err)
		}
	}
	for _, value := range []string{"", "localhost:0", "remote:0", ":0.1", ":0;sh", ":-1", ":999999999999999"} {
		if _, err := localDisplay(value); err == nil {
			t.Fatal("unsafe/unsupported DISPLAY", value)
		}
	}
}
