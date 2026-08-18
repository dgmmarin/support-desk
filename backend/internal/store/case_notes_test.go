package store

import (
	"reflect"
	"testing"
)

// test_FR_M7_09_parse_mentions — @mentions are parsed from note text as data (never executed),
// distinct + first-seen ordered, and an email address is not mistaken for a mention.
func TestFRM709ParseMentions(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []string
	}{
		{"none", "just a plain note", nil},
		{"two distinct", "ping @maria and @jon.doe please", []string{"maria", "jon.doe"}},
		{"dedup", "@lead @lead look here", []string{"lead"}},
		{"start of string", "@ana check this", []string{"ana"}},
		{"email is not a mention", "customer wrote from jon@example.com", nil},
		{"handle with hyphen/underscore", "cc @team-eu and @night_shift", []string{"team-eu", "night_shift"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ParseMentions(c.body); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParseMentions(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}
