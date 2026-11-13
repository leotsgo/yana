package flowchart

import "testing"

func TestTextSize(t *testing.T) {
	ww, h1 := TextSize("WWW", 20)
	iw, _ := TextSize("iii", 20)
	if ww <= iw {
		t.Errorf("WWW %v <= iii %v", ww, iw)
	}
	_, h2 := TextSize("a\nb", 20)
	if h2 != 2*h1 || h1 != 25 {
		t.Errorf("heights %v %v", h1, h2)
	}
}
