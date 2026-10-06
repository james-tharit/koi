package main

import "testing"

func TestPickTOD(t *testing.T) {
	cases := []struct {
		h    int
		want tod
	}{
		{0, todLateNight}, {4, todLateNight},
		{5, todMorning}, {11, todMorning},
		{12, todAfternoon}, {14, todAfternoon},
		{15, todLateAfternoon}, {17, todLateAfternoon},
		{18, todEarlyNight}, {21, todEarlyNight},
		{22, todNight}, {23, todNight},
	}
	for _, c := range cases {
		if got := pickTOD(c.h); got != c.want {
			t.Errorf("pickTOD(%d) = %v, want %v", c.h, got, c.want)
		}
	}
}

func TestParseTOD(t *testing.T) {
	for i, n := range todNames {
		got, ok := parseTOD(n)
		if !ok || int(got) != i {
			t.Errorf("parseTOD(%q) = (%v, %v), want (%d, true)", n, got, ok, i)
		}
	}
	if _, ok := parseTOD("bogus"); ok {
		t.Error("parseTOD(bogus) should fail, got ok=true")
	}
}
