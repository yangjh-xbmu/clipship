package pack

import "testing"

func TestSanitizeBasename(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"report.pdf", "report.pdf"},
		{"my:file.txt", "my_file.txt"},
		{`what?name*.pdf`, "what_name_.pdf"},
		{`<pointy>"brackets|pipe`, "_pointy__brackets_pipe"},
		{"trailing ", "trailing_"},
		{"trailing.", "trailing_"},
		{"  name  ", "  name_"},
		{"", "_"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := SanitizeBasename(tc.in)
			if got != tc.out {
				t.Fatalf("SanitizeBasename(%q) = %q, want %q", tc.in, got, tc.out)
			}
		})
	}
}

func TestResolveName_NoCollision(t *testing.T) {
	seen := make(map[string]bool)
	got := ResolveName(seen, "a.txt")
	if got != "a.txt" {
		t.Fatalf("got %q, want a.txt", got)
	}
	if !seen["a.txt"] {
		t.Fatal("expected seen[a.txt]=true")
	}
}

func TestResolveName_Collision(t *testing.T) {
	seen := map[string]bool{"a.txt": true}
	got := ResolveName(seen, "a.txt")
	if got != "a (1).txt" {
		t.Fatalf("got %q, want a (1).txt", got)
	}
	got2 := ResolveName(seen, "a.txt")
	if got2 != "a (2).txt" {
		t.Fatalf("got %q, want a (2).txt", got2)
	}
}

func TestResolveName_NoExt(t *testing.T) {
	seen := map[string]bool{"LICENSE": true}
	got := ResolveName(seen, "LICENSE")
	if got != "LICENSE (1)" {
		t.Fatalf("got %q, want LICENSE (1)", got)
	}
}
