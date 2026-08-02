package skills

import "testing"

func TestParseFrontmatter(t *testing.T) {
	fm, body, err := ParseFrontmatter([]byte("---\nname: code-review\ndescription: Review source code carefully\nversion: 1.2.0\ninvocation:\n  userInvocable: true\n---\n\n# Review"))
	if err != nil {
		t.Fatal(err)
	}
	if fm.Name != "code-review" || fm.Version != "1.2.0" || !fm.Invocation.UserInvocable {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
	if body != "# Review" {
		t.Fatalf("body = %q", body)
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
