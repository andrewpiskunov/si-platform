package game

import (
	"encoding/json"
	"unicode/utf8"
)

// serializedTextFits bounds escaped output as well as user-visible UTF-8 bytes.
// This keeps role projections below the transport budget even for '<' or controls.
func serializedTextFits(s string, limit int) bool {
	if len(s) > limit {
		return false
	}
	b, _ := json.Marshal(s)
	return len(b) <= limit+2
}
func displayReason(s string) string {
	if serializedTextFits(s, 160) {
		return s
	}
	end := min(len(s), 157)
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	for end > 0 && !serializedTextFits(s[:end]+"…", 160) {
		_, size := utf8.DecodeLastRuneInString(s[:end])
		end -= size
	}
	return s[:end] + "…"
}
