package mcpserver

import "example.com/codex-vision/internal/observation"

// Advisory protocol metadata, not authorization for another action. Never hide
// a real new state or acknowledge pixels the model has not received.
func actionContinuation(r observation.ActionWaitResult, image bool, err error) map[string]any {
	state := "stop"
	if err == nil {
		switch {
		case image:
			state = "inspect_result"
		case r.ReadyConfirmed:
			state = "use_ui_result"
		case r.Posted:
			state = "waiting"
		}
	}
	return map[string]any{
		"next":                         state,
		"repeat_action":                false,
		"immediate_observation_needed": false,
		"visual_state_included":        image,
		"scope":                        "control readiness only when text; visual changes do not verify application semantics",
	}
}
