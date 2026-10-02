package main

import "testing"

func TestParseSize(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		bad  bool
	}{
		{"", 0, false},
		{"0", 0, false},
		{"512", 512, false},
		{"10M", 10 << 20, false},
		{"10m", 10 << 20, false},
		{"1.5G", 1<<30 + 1<<29, false},
		{"2 T", 2 << 40, false},
		{"-5", 0, true},
		{"abc", 0, true},
		{"M", 0, true},
	}
	for _, c := range cases {
		got, err := parseSize(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("parseSize(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSize(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseSize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
