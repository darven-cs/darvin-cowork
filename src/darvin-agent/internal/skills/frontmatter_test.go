package skills

import "testing"

func TestParseFrontmatter(t *testing.T) {
	fm, body, err := ParseFrontmatter([]byte("---\nname: code-review\ndescription: Review source code carefully\nversion: 1.2.0\nuserInvocable: true\ninvocation: manual\nrunAs: subagent\nallowed-tools: read_file, grep\nmodel: sonnet\nread-only: true\nauto-use: prefer\ncost: medium\n---\n\n# Review"))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "code-review" || fm.Version != "1.2.0" || !fm.UserInvocable {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
	if fm.Invocation != "manual" || fm.RunAs != "subagent" || fm.Model != "sonnet" {
		t.Fatalf("frontmatter fields not parsed: %+v", fm)
	}
	if body != "# Review" {
		t.Fatalf("body = %q", body)
	}
}

func TestParseRunAs(t *testing.T) {
	cases := []struct {
		name           string
		runAs, ctx, ag string
		want           string
	}{
		{"explicit subagent", "subagent", "", "", "subagent"},
		{"explicit inline", "inline", "", "", "inline"},
		{"context fork", "", "fork", "", "subagent"},
		{"agent set", "", "", "reviewer", "subagent"},
		{"default inline", "", "", "", "inline"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRunAs(tc.runAs, tc.ctx, tc.ag); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseCSVFrontmatter(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"", nil},
		{"a, b , c", []string{"a", "b", "c"}},
		{"[a,b,c]", []string{"a", "b", "c"}},
		{`["a","b"]`, []string{"a", "b"}},
		{",,,", nil},
	}
	for _, tc := range cases {
		got := parseCSVFrontmatter(tc.raw)
		if len(got) != len(tc.want) {
			t.Fatalf("raw=%q got %v, want %v", tc.raw, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("raw=%q got %v, want %v", tc.raw, got, tc.want)
			}
		}
	}
}

func TestParseBoolFrontmatter(t *testing.T) {
	cases := map[string]bool{
		"true": true, "yes": true, "1": true, "on": true,
		"True": true, "false": false, "0": false, "": false, "yes ": true,
	}
	for raw, want := range cases {
		if got := parseBoolFrontmatter(raw); got != want {
			t.Fatalf("parseBoolFrontmatter(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestParseAutoUse(t *testing.T) {
	cases := map[string]string{
		"off": "off", "suggest": "suggest", "prefer": "prefer", "require": "require",
		"Suggest": "suggest", "OFF": "off",
		"": "", "unknown": "",
	}
	for raw, want := range cases {
		if got := parseAutoUse(raw); got != want {
			t.Fatalf("parseAutoUse(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestParseCost(t *testing.T) {
	if got := parseCost("low"); got != "low" {
		t.Fatalf("low → %q", got)
	}
	if got := parseCost("HIGH"); got != "high" {
		t.Fatalf("HIGH → %q", got)
	}
	if got := parseCost("huge"); got != "" {
		t.Fatalf("huge → %q", got)
	}
}

func TestParseInvocation(t *testing.T) {
	if got := parseInvocation("manual"); got != "manual" {
		t.Fatalf("manual → %q", got)
	}
	if got := parseInvocation("auto"); got != "auto" {
		t.Fatalf("auto → %q", got)
	}
	if got := parseInvocation(""); got != "auto" {
		t.Fatalf("empty → %q", got)
	}
}

func TestParseProfilesFrontmatter(t *testing.T) {
	valid, invalid := parseProfilesFrontmatter("economy, balanced, foo, ECONOMY, delivery")
	if len(valid) != 3 {
		t.Fatalf("valid count = %d, want 3 (got %v)", len(valid), valid)
	}
	if len(invalid) != 1 || invalid[0] != "foo" {
		t.Fatalf("invalid = %v, want [foo]", invalid)
	}
}

func TestParseFrontmatterValidation(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing name", "---\ndescription: A sufficiently long description\n---\nbody"},
		{"missing description", "---\nname: valid-name\n---\nbody"},
		{"missing frontmatter", "body"},
		{"bad yaml", "---\nname: [\n---\nbody"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ParseFrontmatter([]byte(tc.raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestParseFrontmatterUnknownFieldIgnored(t *testing.T) {
	fm, _, err := ParseFrontmatter([]byte("---\nname: testing\ndescription: Suggest useful test coverage\nunknown: value\n---\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "testing" {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
}
