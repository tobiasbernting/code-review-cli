package render

import (
	"reflect"
	"testing"

	"github.com/tobiasbernting/krv/v2/internal/diffparse"
)

func gapFile(t *testing.T, diff string) *diffparse.FileDiff {
	t.Helper()
	files := diffparse.Parse(diff)
	if len(files) != 1 {
		t.Fatalf("parsed %d files, want 1", len(files))
	}
	return files[0]
}

// twoHunks changes line 2 and line 12 of a twenty-line file, with a line
// added in the first hunk so the two sides are numbered differently after it.
const twoHunks = "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
	"@@ -1,3 +1,4 @@\n one\n-two\n+TWO\n+two and a half\n three\n" +
	"@@ -11,3 +12,3 @@\n eleven\n-twelve\n+TWELVE\n thirteen\n"

func TestGapsBetweenHunks(t *testing.T) {
	got := Gaps(gapFile(t, twoHunks), 0)
	want := []Gap{{Index: 1, OldStart: 4, NewStart: 5, Lines: 7}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps = %+v, want %+v", got, want)
	}
}

func TestGapAtFileStart(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -5,3 +5,3 @@\n five\n-six\n+SIX\n seven\n"
	got := Gaps(gapFile(t, diff), 0)
	want := []Gap{{Index: 0, OldStart: 1, NewStart: 1, Lines: 4}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps = %+v, want %+v", got, want)
	}
}

// The Gap after the last hunk is only known once the file's length is: a
// diff does not say whether the file goes on.
func TestGapAtFileEnd(t *testing.T) {
	f := gapFile(t, twoHunks)
	got := Gaps(f, 20)
	want := []Gap{
		{Index: 1, OldStart: 4, NewStart: 5, Lines: 7},
		{Index: 2, OldStart: 14, NewStart: 15, Lines: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps(20) = %+v, want %+v", got, want)
	}
	if got := Gaps(f, 14); len(got) != 1 {
		t.Errorf("Gaps(14) = %+v, want no Gap after a hunk that ends the file", got)
	}
}

func TestNoGapBetweenAdjacentHunks(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -1,3 +1,3 @@\n one\n-two\n+TWO\n three\n" +
		"@@ -4,3 +4,3 @@\n four\n-five\n+FIVE\n six\n"
	if got := Gaps(gapFile(t, diff), 6); len(got) != 0 {
		t.Errorf("Gaps = %+v, want none", got)
	}
}

// A hunk with no lines on one side is numbered from the line before it, so
// the unchanged line it names is still part of the Gap.
func TestGapsAroundAnEmptySide(t *testing.T) {
	diff := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -5,2 +4,0 @@\n-five\n-six\n"
	got := Gaps(gapFile(t, diff), 10)
	want := []Gap{
		{Index: 0, OldStart: 1, NewStart: 1, Lines: 4},
		{Index: 1, OldStart: 7, NewStart: 5, Lines: 6},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Gaps = %+v, want %+v", got, want)
	}
}

func TestMayContinue(t *testing.T) {
	ends := "diff --git a/f.go b/f.go\n--- a/f.go\n+++ b/f.go\n" +
		"@@ -1,2 +1,2 @@\n one\n-two\n+TWO\n\\ No newline at end of file\n"
	if MayContinue(gapFile(t, ends)) {
		t.Error("a hunk ending without a newline was taken for one the file goes on after")
	}
	if !MayContinue(gapFile(t, twoHunks)) {
		t.Error("a hunk in the middle of a file was taken for its end")
	}
}

// Added and deleted files are shown whole, and a binary one not at all.
func TestNoGapsInWholeFiles(t *testing.T) {
	added := "diff --git a/f.go b/f.go\nnew file mode 100644\n--- /dev/null\n+++ b/f.go\n@@ -0,0 +1,2 @@\n+a\n+b\n"
	deleted := "diff --git a/f.go b/f.go\ndeleted file mode 100644\n--- a/f.go\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n"
	binary := "diff --git a/f.png b/f.png\nBinary files a/f.png and b/f.png differ\n"
	for name, diff := range map[string]string{"added": added, "deleted": deleted, "binary": binary} {
		if got := Gaps(gapFile(t, diff), 50); len(got) != 0 {
			t.Errorf("%s: Gaps = %+v, want none", name, got)
		}
	}
}
