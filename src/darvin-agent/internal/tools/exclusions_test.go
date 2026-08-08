// Tests for workspace path exclusion matching.

package tool

import "testing"

func TestExclusionEnvPattern(t *testing.T) {
	excl, err := compileExclusions(DefaultPathExclusions())
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want bool
	}{
		{"/proj/.env", true},
		{"/proj/.env.local", true},
		{"/proj/.env.production", true},
		{"/proj/foo.env", false}, // component-level, not substring
		{"/proj/env", false},
		{"/proj/.git/config", true},
		{"/proj/node_modules/a/b.js", true},
		{"/proj/dist", true},
	}
	for _, c := range cases {
		if _, ok := matchExclusion(excl, c.path); ok != c.want {
			t.Errorf("matchExclusion(%q) = %v, want %v", c.path, ok, c.want)
		}
	}
}

func TestExclusionEmptyPatternIgnored(t *testing.T) {
	excl, err := compileExclusions([]string{"", ".git"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := matchExclusion(excl, "/x/y/.git/z"); !ok {
		t.Error("empty pattern should not disable others")
	}
}
