package flowchart

import "strings"

// TextSize measures s (lines split on \n) in Excalifont at fontSize.
func TextSize(s string, fontSize float64) (w, h float64) {
	lines := strings.Split(s, "\n")
	for _, l := range lines {
		var lw float64
		for _, r := range l {
			lw += runeWidth(r)
		}
		w = max(w, lw*fontSize)
	}
	return w, float64(len(lines)) * fontSize * 1.25
}

func runeWidth(r rune) float64 {
	if w, ok := fontWidths[r]; ok {
		return w
	}
	switch {
	case r >= 0x1100 && r <= 0x11FF, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7AF,
		r >= 0xF900 && r <= 0xFAFF, r >= 0xFE30 && r <= 0xFE6F, r >= 0xFF00 && r <= 0xFFEF,
		r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF:
		return 1.0
	}
	return 0.6
}
