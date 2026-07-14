package main

import (
	"errors"
	"testing"
)

func TestIsCobraUsageError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"accepts 1 arg(s), received 0", true},
		{"unknown command \"foo\" for \"tabstack\"", true},
		{"unknown flag: --bogus", true},
		{"unknown shorthand flag: 'z'", true},
		{"required flag(s) \"schema\" not set", true},
		{"invalid argument \"x\" for \"--timeout\"", true},
		{"api error (400): bad", false},
		{"some runtime failure", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isCobraUsageError(errors.New(tc.msg)); got != tc.want {
			t.Errorf("isCobraUsageError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}
