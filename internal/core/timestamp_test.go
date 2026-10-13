package core

import "testing"

func TestParseTimestamp(t *testing.T) {
	for text, want := range map[string]int64{
		"1700000000000": 1700000000000,
		"1791880455533": 1791880455533,
		"1799999999999": 1799999999999,
	} {
		got, ok := ParseTimestamp(text)
		if !ok || got.UnixMilli() != want {
			t.Errorf("ParseTimestamp(%q) = %v, %v; want %d milliseconds", text, got, ok, want)
		}
	}
}

func TestParseTimestampRejectsNonExactText(t *testing.T) {
	for _, text := range []string{
		"", "17", "1791880455", "179188045553", "17918804555330",
		"1691880455533", "1891880455533", "0791880455533",
		" 1791880455533", "1791880455533 ", "1791880455533\n",
		"timestamp=1791880455533", "http://1791880455533", "1791880455533/path",
		"+1791880455533", "1791880455.533", "17918804555a3", "179188045553\x00",
		"１７９１８８０４５５５３３", "179188045553٣",
	} {
		if _, ok := ParseTimestamp(text); ok {
			t.Errorf("ParseTimestamp(%q) accepted non-exact timestamp", text)
		}
	}
}
