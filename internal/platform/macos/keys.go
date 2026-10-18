package macos

import "strings"

type macKey struct {
	code uint16
	flag uint64
}

var keyCodes = map[string]macKey{
	"A": {0, 0}, "S": {1, 0}, "D": {2, 0}, "F": {3, 0}, "H": {4, 0}, "G": {5, 0}, "Z": {6, 0}, "X": {7, 0}, "C": {8, 0}, "V": {9, 0}, "B": {11, 0}, "Q": {12, 0}, "W": {13, 0}, "E": {14, 0}, "R": {15, 0}, "Y": {16, 0}, "T": {17, 0}, "1": {18, 0}, "2": {19, 0}, "3": {20, 0}, "4": {21, 0}, "6": {22, 0}, "5": {23, 0}, "9": {25, 0}, "7": {26, 0}, "8": {28, 0}, "0": {29, 0}, "O": {31, 0}, "U": {32, 0}, "I": {34, 0}, "P": {35, 0}, "ENTER": {36, 0}, "L": {37, 0}, "J": {38, 0}, "K": {40, 0}, "N": {45, 0}, "M": {46, 0}, "TAB": {48, 0}, "SPACE": {49, 0}, "BACKSPACE": {51, 0}, "ESCAPE": {53, 0}, "WIN": {55, 1 << 20}, "CMD": {55, 1 << 20}, "COMMAND": {55, 1 << 20}, "SHIFT": {56, 1 << 17}, "ALT": {58, 1 << 19}, "OPTION": {58, 1 << 19}, "CTRL": {59, 1 << 18}, "F1": {122, 0}, "F2": {120, 0}, "F3": {99, 0}, "F4": {118, 0}, "F5": {96, 0}, "F6": {97, 0}, "F7": {98, 0}, "F8": {100, 0}, "F9": {101, 0}, "F10": {109, 0}, "F11": {103, 0}, "F12": {111, 0}, "HOME": {115, 0}, "END": {119, 0}, "PAGEUP": {116, 0}, "PAGEDOWN": {121, 0}, "DELETE": {117, 0}, "INSERT": {114, 0}, "LEFT": {123, 0}, "RIGHT": {124, 0}, "DOWN": {125, 0}, "UP": {126, 0},
}

func orderKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if keyCodes[strings.ToUpper(k)].flag != 0 {
			out = append(out, k)
		}
	}
	for _, k := range keys {
		if keyCodes[strings.ToUpper(k)].flag == 0 {
			out = append(out, k)
		}
	}
	return out
}
