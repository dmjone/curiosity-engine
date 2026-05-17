package engine

import "testing"

// TestExtractJSON covers the cases the model actually produces: fenced JSON,
// surrounding prose, braces inside strings, and missing JSON.
func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"fenced", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"prose around", `here you go: {"x":1} thanks`, `{"x":1}`},
		{"brace in string", `{"x":"}"}`, `{"x":"}"}`},
		{"nested", `{"o":{"k":2}}`, `{"o":{"k":2}}`},
		{"escaped quote", `{"x":"a\"b"}`, `{"x":"a\"b"}`},
		{"none", "no json at all", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := extractJSON(c.in); got != c.want {
				t.Errorf("extractJSON(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestSubjectByTopic guards the syllabus lookup the channel bindings rely on.
func TestSubjectByTopic(t *testing.T) {
	if s, ok := SubjectByTopic("operating-systems"); !ok || s.DisplayName == "" {
		t.Fatalf("operating-systems should resolve to a subject")
	}
	if _, ok := SubjectByTopic("not-a-subject"); ok {
		t.Errorf("unknown topic should not resolve")
	}
}
