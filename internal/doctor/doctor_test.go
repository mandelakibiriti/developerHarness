package doctor

import "testing"

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in       string
		maj, min int
		ok       bool
	}{
		{"v26.13.0", 26, 13, true},
		{"Python 3.13.7", 3, 13, true},
		{"v22.0.0", 22, 0, true},
		{"garbage", 0, 0, false},
	}
	for _, c := range cases {
		maj, min, err := parseVersion(c.in)
		if c.ok {
			if err != nil || maj != c.maj || min != c.min {
				t.Errorf("parseVersion(%q) = %d,%d,%v; want %d,%d", c.in, maj, min, err, c.maj, c.min)
			}
		} else if err == nil {
			t.Errorf("parseVersion(%q) should fail", c.in)
		}
	}
}
