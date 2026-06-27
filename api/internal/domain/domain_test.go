package domain

import "testing"

func TestValidateAlias(t *testing.T) {
	cases := []struct {
		name  string
		alias string
		valid bool
	}{
		{"typical", "skg_news", true},
		{"with dots and dashes", "a.b-c_d", true},
		{"leading underscore", "_news", true},
		{"min length", "ab", true},
		{"max length", "abcdefghijklmnopqrstuvwx", true}, // 24
		{"uppercase allowed", "SKG_News", true},
		{"too short", "a", false},
		{"too long", "abcdefghijklmnopqrstuvwxy", false}, // 25
		{"leading digit", "1news", false},
		{"leading dot", ".news", false},
		{"leading dash", "-news", false},
		{"space", "skg news", false},
		{"slash", "skg/news", false},
		{"unicode", "ние", false},
		{"reserved ch_ prefix", "ch_abcdef", false},
		{"reserved ch_ prefix mixed case", "Ch_abcdef", false},
		{"empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateAlias(c.alias)
			if c.valid && err != nil {
				t.Fatalf("ValidateAlias(%q) = %v, want nil", c.alias, err)
			}
			if !c.valid && err == nil {
				t.Fatalf("ValidateAlias(%q) = nil, want error", c.alias)
			}
		})
	}
}
