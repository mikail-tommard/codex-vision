package input

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

var keys = map[string]uint16{"ENTER": 13, "TAB": 9, "ESCAPE": 27, "SPACE": 32, "BACKSPACE": 8, "DELETE": 46, "INSERT": 45, "HOME": 36, "END": 35, "PAGEUP": 33, "PAGEDOWN": 34, "LEFT": 37, "UP": 38, "RIGHT": 39, "DOWN": 40, "CTRL": 17, "SHIFT": 16, "ALT": 18, "WIN": 91, "CMD": 91, "COMMAND": 91, "OPTION": 18}

func Key(s string) (uint16, error) {
	s = strings.ToUpper(s)
	if len(s) == 1 && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= '0' && s[0] <= '9')) {
		return uint16(s[0]), nil
	}
	if k, ok := keys[s]; ok {
		return k, nil
	}
	for i := 1; i <= 12; i++ {
		if s == fmt.Sprintf("F%d", i) {
			return uint16(111 + i), nil
		}
	}
	return 0, fmt.Errorf("unsupported key; use A-Z, 0-9, F1-F12, ENTER, TAB, ESCAPE, SPACE, BACKSPACE, DELETE, INSERT, HOME, END, PAGEUP, PAGEDOWN, arrows, CTRL, SHIFT, ALT, WIN")
}
func ValidateKeys(ks []string) error {
	if len(ks) < 1 || len(ks) > 5 {
		return fmt.Errorf("key combination must contain 1..5 keys")
	}
	seen := map[uint16]bool{}
	for _, s := range ks {
		k, e := Key(s)
		if e != nil {
			return e
		}
		if seen[k] {
			return fmt.Errorf("duplicate key")
		}
		seen[k] = true
	}
	return nil
}
func ValidateText(s string) error {
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > 4096 || len(s) == 0 || strings.ContainsRune(s, 0) {
		return fmt.Errorf("text must contain 1..4096 valid Unicode code points without NUL")
	}
	return nil
}
