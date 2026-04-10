package diff

import "testing"

const sampleDiff = `diff --git a/internal/foo.go b/internal/foo.go
index abc123..def456 100644
--- a/internal/foo.go
+++ b/internal/foo.go
@@ -10,6 +10,8 @@ func Foo() {
 	x := 1
-	return x
+	y := x + 1
+	return y
 }
diff --git a/internal/bar.go b/internal/bar.go
new file mode 100644
--- /dev/null
+++ b/internal/bar.go
@@ -0,0 +1,5 @@
+package internal
+
+func Bar() int {
+	return 42
+}
`

func TestParseGitDiffModifiedFile(t *testing.T) {
	files := ParseGitDiff(sampleDiff)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	f := files[0]
	if f.NewPath != "internal/foo.go" || f.Status != "M" {
		t.Fatalf("unexpected file diff: %+v", f)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("expected 1 hunk, got %d", len(f.Hunks))
	}
}

func TestParseGitDiffNewFile(t *testing.T) {
	files := ParseGitDiff(sampleDiff)
	f := files[1]
	if f.NewPath != "internal/bar.go" || f.Status != "A" {
		t.Fatalf("unexpected file diff: %+v", f)
	}
}

func TestParseGitDiffDeletedFile(t *testing.T) {
	const deleteDiff = `diff --git a/gone.go b/gone.go
deleted file mode 100644
--- a/gone.go
+++ /dev/null
@@ -1,3 +0,0 @@
-package main
-
-func main() {}
`
	files := ParseGitDiff(deleteDiff)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Status != "D" || f.OldPath != "gone.go" || f.NewPath != "" {
		t.Fatalf("unexpected deleted diff: %+v", f)
	}
}

func TestParseGitDiffRename(t *testing.T) {
	const renameDiff = `diff --git a/old/path.go b/new/path.go
rename from old/path.go
rename to new/path.go
`
	files := ParseGitDiff(renameDiff)
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	f := files[0]
	if f.Status != "R" || f.OldPath != "old/path.go" || f.NewPath != "new/path.go" {
		t.Fatalf("unexpected rename diff: %+v", f)
	}
}

func TestDisplayPath(t *testing.T) {
	tests := []struct {
		name string
		file FileDiff
		want string
	}{
		{name: "modified", file: FileDiff{Status: "M", NewPath: "foo.go"}, want: "foo.go"},
		{name: "deleted", file: FileDiff{Status: "D", OldPath: "gone.go"}, want: "gone.go"},
		{name: "rename", file: FileDiff{Status: "R", OldPath: "old.go", NewPath: "new.go"}, want: "old.go → new.go"},
	}
	for _, tt := range tests {
		if got := DisplayPath(tt.file); got != tt.want {
			t.Fatalf("%s: got %q want %q", tt.name, got, tt.want)
		}
	}
}

func TestLineHelpers(t *testing.T) {
	file := FileDiff{Hunks: []Hunk{{NewStart: 42}}}
	if got := FirstChangedLine(file); got != 42 {
		t.Fatalf("got %d want 42", got)
	}
	hunk := Hunk{NewStart: 10, Lines: []HunkLine{{Type: ' ', Content: "context"}, {Type: '-', Content: "removed"}, {Type: '+', Content: "added"}}}
	if got := HunkChangedLine(hunk); got != 11 {
		t.Fatalf("got %d want 11", got)
	}
}
