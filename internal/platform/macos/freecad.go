package macos

// FreeCAD's installed launcher starts a different executable for its GUI.
// This exact pair is used ONLY to confirm a requested launch after Launch
// Services validated the main bundle executable. It grants no input access:
// the GUI runtime still needs its own allowed_processes entry.
func freeCADLaunchRuntime(launcher, actual string) bool {
	return launcher == "/Applications/FreeCAD.app/Contents/MacOS/FreeCAD" &&
		actual == "/Applications/FreeCAD.app/Contents/Resources/bin/freecad"
}
