package platform

import "testing"

func TestBoundsAndMapping(t *testing.T) {
	ms := []Monitor{{Bounds: Rect{-1920, -200, 1920, 1080}}, {Bounds: Rect{0, 0, 2560, 1440}}}
	b, e := Bounds(ms)
	if e != nil || b != (Rect{-1920, -200, 4480, 1640}) {
		t.Fatal(b, e)
	}
	x, y, e := Normalize(-1920, -200, b)
	if e != nil || x != 0 || y != 0 {
		t.Fatal(x, y, e)
	}
	x, y, e = Normalize(2559, 1439, b)
	if e != nil || x != 65535 || y != 65535 {
		t.Fatal(x, y, e)
	}
	if _, _, e = Normalize(2560, 0, b); e == nil {
		t.Fatal("out of bounds accepted")
	}
	if b.Includes(Rect{-1921, 0, 10, 10}) || b.Includes(Rect{0, 0, -1, 1}) {
		t.Fatal("invalid region")
	}
	if _, e = Bounds(nil); e == nil {
		t.Fatal("missing monitors")
	}
}
func FuzzNormalize(f *testing.F) {
	f.Add(0, 0)
	f.Add(-320, 40)
	f.Fuzz(func(t *testing.T, x, y int) {
		b := Rect{-320, -100, 960, 580}
		nx, ny, e := Normalize(x, y, b)
		if b.Contains(x, y) {
			if e != nil || nx < 0 || nx > 65535 || ny < 0 || ny > 65535 {
				t.Fatalf("invalid normalization %d %d %v", nx, ny, e)
			}
		} else if e == nil {
			t.Fatal("accepted outside point")
		}
	})
}
