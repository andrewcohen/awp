package workspace

import "testing"

func TestNormalizeName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "Add Auth", want: "add-auth"},
		{in: "feature_one", want: "feature-one"},
		{in: " A@@B  ", want: "a-b"},
	}

	for _, tt := range tests {
		got, err := NormalizeName(tt.in)
		if err != nil {
			t.Fatalf("NormalizeName(%q) returned error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("NormalizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestNormalizeNameEmpty(t *testing.T) {
	if _, err := NormalizeName("---"); err == nil {
		t.Fatal("expected error for empty normalized name")
	}
}

func TestReviewWorkspaceName(t *testing.T) {
	if got := ReviewWorkspaceName(42, "feat/x"); got != "pr-42-feat/x" {
		t.Fatalf("ReviewWorkspaceName = %q, want pr-42-feat/x", got)
	}
	// The deck normalizes the result before matching; confirm it lands where
	// the workspace service would put a review checkout.
	got, err := NormalizeName(ReviewWorkspaceName(42, "feat/x"))
	if err != nil {
		t.Fatalf("normalize returned error: %v", err)
	}
	if got != "pr-42-feat-x" {
		t.Fatalf("normalized review name = %q, want pr-42-feat-x", got)
	}
}
