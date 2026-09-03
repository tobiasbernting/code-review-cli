package diffparse

import "testing"

const modifiedDiff = `diff --git a/main.go b/main.go
index 1111111..2222222 100644
--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@ func main()
 package main
 
-func main() {}
+func main() {
+	println("hi")
+}
`

func TestParseModified(t *testing.T) {
	files := Parse(modifiedDiff)
	if len(files) != 1 {
		t.Fatalf("got %d files, want 1", len(files))
	}
	f := files[0]
	if f.Path() != "main.go" {
		t.Errorf("path = %q, want main.go", f.Path())
	}
	if f.Status != Modified {
		t.Errorf("status = %v, want modified", f.Status)
	}

	hunks := f.Hunks()
	if len(hunks) != 1 {
		t.Fatalf("got %d hunks, want 1", len(hunks))
	}
	h := hunks[0]
	if h.Section != "func main()" {
		t.Errorf("section = %q", h.Section)
	}
	if h.OldStart != 1 || h.OldLines != 5 || h.NewStart != 1 || h.NewLines != 6 {
		t.Errorf("bad hunk range: %+v", h)
	}
	if len(h.Lines) != 6 {
		t.Fatalf("got %d lines, want 6", len(h.Lines))
	}

	// Line numbering must advance independently on each side.
	want := []struct {
		kind   LineKind
		oldNum int
		newNum int
	}{
		{KindContext, 1, 1},
		{KindContext, 2, 2},
		{KindDel, 3, 0},
		{KindAdd, 0, 3},
		{KindAdd, 0, 4},
		{KindAdd, 0, 5},
	}
	for i, w := range want {
		got := h.Lines[i]
		if got.Kind != w.kind || got.OldNum != w.oldNum || got.NewNum != w.newNum {
			t.Errorf("line %d = %+v, want kind=%v old=%d new=%d", i, got, w.kind, w.oldNum, w.newNum)
		}
	}
}

func TestParseStatuses(t *testing.T) {
	cases := []struct {
		name     string
		diff     string
		wantPath string
		wantOld  string
		want     Status
		binary   bool
	}{
		{
			name:     "added",
			diff:     "diff --git a/new.txt b/new.txt\nnew file mode 100644\n--- /dev/null\n+++ b/new.txt\n@@ -0,0 +1 @@\n+hello\n",
			wantPath: "new.txt", want: Added,
		},
		{
			name:     "deleted",
			diff:     "diff --git a/old.txt b/old.txt\ndeleted file mode 100644\n--- a/old.txt\n+++ /dev/null\n@@ -1 +0,0 @@\n-bye\n",
			wantPath: "old.txt", want: Deleted,
		},
		{
			name:     "renamed",
			diff:     "diff --git a/a.go b/b.go\nsimilarity index 92%\nrename from a.go\nrename to b.go\n--- a/a.go\n+++ b/b.go\n@@ -1 +1 @@\n-x\n+y\n",
			wantPath: "b.go", wantOld: "a.go", want: Renamed,
		},
		{
			name:     "binary",
			diff:     "diff --git a/logo.png b/logo.png\nindex 111..222 100644\nBinary files a/logo.png and b/logo.png differ\n",
			wantPath: "logo.png", want: Modified, binary: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := Parse(tc.diff)
			if len(files) != 1 {
				t.Fatalf("got %d files, want 1", len(files))
			}
			f := files[0]
			if f.Path() != tc.wantPath {
				t.Errorf("path = %q, want %q", f.Path(), tc.wantPath)
			}
			if f.Status != tc.want {
				t.Errorf("status = %v, want %v", f.Status, tc.want)
			}
			if f.IsBinary != tc.binary {
				t.Errorf("binary = %v, want %v", f.IsBinary, tc.binary)
			}
			if tc.wantOld != "" && f.OldPath != tc.wantOld {
				t.Errorf("old path = %q, want %q", f.OldPath, tc.wantOld)
			}
		})
	}
}

func TestParseMultipleFilesAndHunks(t *testing.T) {
	diff := modifiedDiff + `diff --git a/other.txt b/other.txt
index 3333333..4444444 100644
--- a/other.txt
+++ b/other.txt
@@ -1,2 +1,2 @@
-a
+b
@@ -10,2 +10,2 @@
-c
+d
`
	files := Parse(diff)
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if got := len(files[1].Hunks()); got != 2 {
		t.Fatalf("got %d hunks, want 2", got)
	}
	if h := files[1].Hunks()[1]; h.OldStart != 10 {
		t.Errorf("second hunk starts at %d, want 10", h.OldStart)
	}
}

func TestParseNoNewlineMarker(t *testing.T) {
	diff := "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n"
	f := Parse(diff)[0]
	lines := f.Hunks()[0].Lines
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !lines[0].NoNewline {
		t.Error("expected the deleted line to carry the no-newline marker")
	}
	if lines[1].NoNewline {
		t.Error("marker leaked onto the added line")
	}
}

func TestParseQuotedPath(t *testing.T) {
	diff := "diff --git \"a/f\\303\\266o.txt\" \"b/f\\303\\266o.txt\"\n--- \"a/f\\303\\266o.txt\"\n+++ \"b/f\\303\\266o.txt\"\n@@ -1 +1 @@\n-a\n+b\n"
	f := Parse(diff)[0]
	if f.Path() != "föo.txt" {
		t.Errorf("path = %q, want föo.txt", f.Path())
	}
}

func TestParseIndexBlobs(t *testing.T) {
	f := Parse(modifiedDiff)[0]
	if f.OldBlob != "1111111" || f.NewBlob != "2222222" {
		t.Errorf("blobs = %q..%q, want 1111111..2222222", f.OldBlob, f.NewBlob)
	}

	// A new file's index line has no mode suffix and an all-zero old hash.
	added := Parse("diff --git a/n.txt b/n.txt\nnew file mode 100644\nindex 0000000..abc1234\n--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1 @@\n+x\n")[0]
	if added.NewBlob != "abc1234" {
		t.Errorf("new file blob = %q, want abc1234", added.NewBlob)
	}
}

func TestFillStatsCountsFromHunks(t *testing.T) {
	files := Parse(modifiedDiff)
	FillStats(files)
	if files[0].Additions != 3 || files[0].Deletions != 1 {
		t.Errorf("stats = +%d −%d, want +3 −1", files[0].Additions, files[0].Deletions)
	}

	// Existing counts win: --numstat is authoritative where it ran.
	pre := Parse(modifiedDiff)
	pre[0].Additions, pre[0].Deletions = 99, 98
	FillStats(pre)
	if pre[0].Additions != 99 {
		t.Errorf("FillStats overwrote numstat counts: +%d", pre[0].Additions)
	}
}
