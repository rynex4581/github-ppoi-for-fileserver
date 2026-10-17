package version

import "testing"

func TestParseAndCompare(t *testing.T) {
	cases := []struct {
		in      string
		x, yz   int
		letter  int
		numeric string
	}{
		{"0.10", 0, 10, 0, "0.10.0.0"},
		{"0.11A", 0, 11, 1, "0.11.1.0"},
		{"0.11Z", 0, 11, 26, "0.11.26.0"},
		{"1.00", 1, 0, 0, "1.0.0.0"},
		{"2.35C", 2, 35, 3, "2.35.3.0"},
	}
	for _, c := range cases {
		p, err := Parse(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if p.X != c.x || p.YZ != c.yz || p.Letter != c.letter {
			t.Errorf("%s の分け方が違う: %+v", c.in, p)
		}
		a, b, d, e := p.Numeric()
		got := formatNumeric(a, b, d, e)
		if got != c.numeric {
			t.Errorf("%s の数値4組が違う: %s", c.in, got)
		}
		if p.String() != c.in {
			t.Errorf("%s を戻すと %s になる", c.in, p.String())
		}
		if FromNumeric(a, b, d).String() != c.in {
			t.Errorf("%s を数値から戻すと %s になる", c.in, FromNumeric(a, b, d).String())
		}
	}
	for _, bad := range []string{"", "0.1", "0.100", "0.10a", "v0.10", "0.10AA"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q が通った", bad)
		}
	}
	order := []string{"0.10", "0.10A", "0.10B", "0.11", "0.11Z", "0.12", "0.20", "1.00", "1.00A", "2.00"}
	for i := 1; i < len(order); i++ {
		a, _ := Parse(order[i-1])
		b, _ := Parse(order[i])
		if Compare(a, b) >= 0 || Compare(b, a) <= 0 {
			t.Errorf("%s < %s になっていない", order[i-1], order[i])
		}
	}
	if Compare(Current, Current) != 0 {
		t.Error("同じ版の比較が 0 でない")
	}
}

func formatNumeric(a, b, c, d int) string {
	return itoa(a) + "." + itoa(b) + "." + itoa(c) + "." + itoa(d)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
