package flowchart

import "testing"

func TestKeyBetween(t *testing.T) {
	for _, c := range [][3]string{
		{"", "", "a0"}, {"a0", "", "a1"}, {"a9", "", "aA"}, {"az", "", "b00"},
		{"a0", "a1", "a0V"}, {"", "a0", "Zz"},
	} {
		got, err := keyBetween(c[0], c[1])
		if err != nil || got != c[2] {
			t.Errorf("keyBetween(%q,%q)=%q,%v want %q", c[0], c[1], got, err, c[2])
		}
	}
	if _, err := keyBetween("a1", "a0"); err == nil {
		t.Error("want error for a1 >= a0")
	}
}

func TestNKeysBetween(t *testing.T) {
	for _, ab := range [][2]string{{"a0", ""}, {"", "a0"}, {"a0", "a1"}, {"", ""}} {
		ks, err := nKeysBetween(ab[0], ab[1], 20)
		if err != nil || len(ks) != 20 {
			t.Fatalf("%v: %v %d", ab, err, len(ks))
		}
		prev := ab[0]
		for _, k := range ks {
			if k <= prev || (ab[1] != "" && k >= ab[1]) {
				t.Fatalf("%v: %q not after %q / before %q", ab, k, prev, ab[1])
			}
			prev = k
		}
	}
}
