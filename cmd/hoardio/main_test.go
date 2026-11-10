package main

import (
	"flag"
	"os"
	"testing"
)

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
		{"10MB", 10 << 20, false},
		{"1.5gb", 1<<30 + 1<<29, false},
		{"5B", 5, false},
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

func TestTextForced(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"no flags", nil, false},
		{"top set", []string{"-top", "20"}, true},
		{"min-size set", []string{"-min-size", "1G"}, true},
		{"tree-depth set", []string{"-tree-depth", "3"}, true},
		{"unrelated flag set", []string{"-workers", "4"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := flag.NewFlagSet("hoardio", flag.ContinueOnError)
			fs.Int("top", 30, "")
			fs.String("min-size", "0", "")
			fs.Int("tree-depth", 2, "")
			fs.Int("workers", 0, "")

			if err := fs.Parse(c.args); err != nil {
				t.Fatal(err)
			}

			if got := textForced(fs); got != c.want {
				t.Errorf("textForced(%v) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

func TestIsTerminalRejectsDevNull(t *testing.T) {
	// /dev/null has the char-device bit set; a Stat-based check wrongly calls
	// it a terminal and opens an invisible TUI for `hoardio > /dev/null`.
	devnull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = devnull.Close() }()

	if isTerminal(devnull) {
		t.Errorf("isTerminal(%s) = true, want false", os.DevNull)
	}
}
