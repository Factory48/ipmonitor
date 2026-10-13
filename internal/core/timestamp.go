package core

import "time"

// ParseTimestamp accepts only a complete 13-digit ASCII millisecond timestamp
// starting with 17. Whitespace, prefixes, suffixes and seconds are not accepted.
func ParseTimestamp(text string) (time.Time, bool) {
	if len(text) != 13 || text[0] != '1' || text[1] != '7' {
		return time.Time{}, false
	}
	var millis int64
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			return time.Time{}, false
		}
		millis = millis*10 + int64(text[i]-'0')
	}
	return time.UnixMilli(millis), true
}
