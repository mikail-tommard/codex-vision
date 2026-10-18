package macos

import (
	"example.com/codex-vision/internal/input"
	"example.com/codex-vision/internal/platform"
	"strings"
	"testing"
)

func TestReviewEscapesUntrustedContent(t *testing.T) {
	a := platform.Action{Tool: "keyboard_type", Text: "\"\nend run\ndo shell script \"touch bad\"\nПривет\u202e"}
	script := reviewScript(a, platform.Window{ID: "id", Process: "/Applications/Test.app"})
	if strings.Contains(script, "\ndo shell script") {
		t.Fatal("script injection")
	}
	if !strings.Contains(script, `alert.addButtonWithTitle("Cancel")`) || !strings.Contains(script, `text.editable = false`) {
		t.Fatal("safe dialog defaults missing")
	}
	if strings.ContainsRune(script, '\u202e') {
		t.Fatal("untrusted bidi control not escaped")
	}
}
func TestMacKeyCoverageAndOrdering(t *testing.T) {
	for _, k := range []string{"CMD", "COMMAND", "WIN", "CTRL", "OPTION", "A", "Z", "0", "9", "ENTER", "LEFT", "F12"} {
		if _, e := input.Key(k); e != nil {
			t.Fatal(e)
		}
		if _, ok := keyCodes[k]; !ok {
			t.Fatal(k)
		}
	}
	keys := orderKeys([]string{"C", "CMD", "SHIFT"})
	if keys[0] != "CMD" || keys[1] != "SHIFT" || keys[2] != "C" {
		t.Fatal(keys)
	}
	if input.ValidateKeys([]string{"CMD", "WIN"}) == nil {
		t.Fatal("aliases must not bypass duplicate key validation")
	}
}
