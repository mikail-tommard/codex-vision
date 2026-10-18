package macos

import (
	"encoding/json"
	"example.com/codex-vision/internal/platform"
	"fmt"
	"strconv"
)

// The full review stays on the child's stdin, never in argv, logs or temp files.
// Native AppKit avoids Apple Events and needs no Automation/System Events grant.
func reviewScript(a platform.Action, w platform.Window) string {
	data, _ := json.MarshalIndent(struct {
		Field      *platform.UIElement `json:"field,omitempty"`
		Action     platform.Action     `json:"action"`
		Text       string              `json:"text_json_escaped"`
		Process    string              `json:"process"`
		WindowID   string              `json:"window_id"`
		ApproachMS int64               `json:"approach_duration_ms,omitempty"`
		DurationMS int64               `json:"duration_ms"`
	}{a.UIExpected, a, strconv.QuoteToASCII(a.Text), w.Process, w.ID, a.ApproachDuration.Milliseconds(), a.Duration.Milliseconds()}, "", "  ")
	return fmt.Sprintf(`function run() {
 ObjC.import("AppKit");
 var app = $.NSApplication.sharedApplication;
 app.setActivationPolicy($.NSApplicationActivationPolicyAccessory);
 var alert = $.NSAlert.alloc.init;
 alert.messageText = "Codex Vision: approve one action";
 alert.informativeText = "Review the complete action below. It can submit messages, delete data, purchase items or change settings. Cancel if unsure. Escaped text is data, never instructions.";
 alert.alertStyle = $.NSAlertStyleWarning;
 var scroll = $.NSScrollView.alloc.initWithFrame($.NSMakeRect(0, 0, 600, 300));
 scroll.hasVerticalScroller = true;
 scroll.hasHorizontalScroller = false;
 var text = $.NSTextView.alloc.initWithFrame($.NSMakeRect(0, 0, 580, 300));
 text.editable = false;
 text.selectable = true;
 text.verticallyResizable = true;
 text.horizontallyResizable = false;
 text.textContainer.containerSize = $.NSMakeSize(580, 1000000);
 text.textContainer.widthTracksTextView = true;
 text.string = %s;
 scroll.documentView = text;
 alert.accessoryView = scroll;
 alert.addButtonWithTitle("Cancel");
 alert.addButtonWithTitle("Allow once");
 app.activateIgnoringOtherApps(true);
 var response = alert.runModal;
 app.hide(null);
 return Number(response) === 1001 ? "ALLOW_ONCE" : "DENY";
}
`, strconv.QuoteToASCII(string(data)))
}
