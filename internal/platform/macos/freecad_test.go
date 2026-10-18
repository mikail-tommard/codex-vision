package macos

import "testing"

func TestFreeCADLaunchRuntimeExactPairOnly(t *testing.T) {
	const launcher = "/Applications/FreeCAD.app/Contents/MacOS/FreeCAD"
	const runtime = "/Applications/FreeCAD.app/Contents/Resources/bin/freecad"
	if !freeCADLaunchRuntime(launcher, runtime) {
		t.Fatal("FreeCAD GUI not recognized")
	}
	for _, pair := range [][2]string{
		{runtime, launcher},
		{launcher, "/private/tmp/FreeCAD.app/Contents/Resources/bin/freecad"},
		{launcher, "/Applications/FreeCAD.app/Contents/Resources/bin/python"},
		{launcher, runtime + "-helper"},
		{launcher, "/Applications/FreeCAD.app/Contents/Resources/bin/../bin/freecad"},
		{"/Applications/Other.app/Contents/MacOS/FreeCAD", runtime},
	} {
		if freeCADLaunchRuntime(pair[0], pair[1]) {
			t.Fatalf("accepted unrelated pair: %q", pair)
		}
	}
}
