package ghsrc

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const revisionBase = "1111111111111111111111111111111111111111"
const revisionHead = "2222222222222222222222222222222222222222"

type revisionFixtureFile struct {
	body, mode, kind, sha string
}

func revisionFixture(t *testing.T, base, head map[string]revisionFixtureFile) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
[ "$1" = api ] || exit 2
printf '%s\n' "$2" >> "$CRV_REVISION_FIXTURES/calls"
id="${2##*/}"
case "$2" in
  repos/acme/repo/git/trees/*) id="${id%%\?*}"; cat "$CRV_REVISION_FIXTURES/tree-$id.json" ;;
  repos/acme/repo/git/blobs/*) cat "$CRV_REVISION_FIXTURES/blob-$id.json" ;;
  *) exit 3 ;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CRV_REVISION_FIXTURES", dir)
	for ref, files := range map[string]map[string]revisionFixtureFile{revisionBase: base, revisionHead: head} {
		entries := make([]revisionEntry, 0, len(files))
		for path, file := range files {
			mode, kind := file.mode, file.kind
			if mode == "" {
				mode = "100644"
			}
			if kind == "" {
				kind = "blob"
			}
			id := file.sha
			if id == "" {
				id = fmt.Sprintf("%x", sha1.Sum([]byte(fmt.Sprintf("blob %d\x00%s", len(file.body), file.body))))
			}
			entries = append(entries, revisionEntry{Path: path, Mode: mode, Type: kind, SHA: id})
			if kind == "blob" {
				revisionWriteJSON(t, filepath.Join(dir, "blob-"+id+".json"), map[string]any{
					"sha": id, "encoding": "base64", "content": base64.StdEncoding.EncodeToString([]byte(file.body)), "size": len(file.body),
				})
			}
		}
		revisionWriteJSON(t, filepath.Join(dir, "tree-"+ref+".json"), map[string]any{"sha": ref, "tree": entries, "truncated": false})
	}
	return dir
}

func revisionWriteJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCompareRevisionsExactSnapshotsAfterDivergence(t *testing.T) {
	// These snapshots represent divergent commits: the reviewed branch had
	// review-only.go, but a force push removed it. Comparing merge base -> head
	// would never show that removal. Snapshot comparison must show it.
	dir := revisionFixture(t, map[string]revisionFixtureFile{
		"review-only.go":    {body: "reviewed change\n"},
		"src/space name.go": {body: "before review\n"},
		"unchanged.go":      {body: "unchanged\n"},
		"old name.go":       {body: "rename me exactly\n"},
		"image.bin":         {body: "\x00old\xff"},
		"tool":              {body: "script\n"},
		"submodule":         {kind: "commit", mode: "160000", sha: revisionBase},
	}, map[string]revisionFixtureFile{
		"src/space name.go": {body: "after fix\n"},
		"unchanged.go":      {body: "unchanged\n"},
		"new name.go":       {body: "rename me exactly\n"},
		"image.bin":         {body: "\x00new\xfe"},
		"tool":              {body: "script\n", mode: "100755"},
		"submodule":         {kind: "commit", mode: "160000", sha: revisionHead},
	})
	got, err := (Client{}).CompareRevisions("acme/repo", revisionBase, revisionHead)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"-reviewed change", "-before review", "+after fix", "GIT binary patch", "rename from old name.go", "rename to new name.go", "old mode 100644", "new mode 100755", "-Subproject commit " + revisionBase, "+Subproject commit " + revisionHead} {
		if !strings.Contains(got.Diff, want) {
			t.Errorf("diff missing %q:\n%s", want, got.Diff)
		}
	}
	if strings.Contains(got.Diff, "unchanged.go") || strings.Contains(got.Diff, "crv-revision-") {
		t.Errorf("diff includes unchanged or temporary paths: %s", got.Diff)
	}
	files := map[string]RevisionFile{}
	for _, file := range got.Files {
		files[file.Path] = file
	}
	if len(files) != 6 || files["review-only.go"].Status != "removed" || files["new name.go"].PreviousPath != "old name.go" || files["src/space name.go"].Status != "modified" {
		t.Errorf("files = %+v", got.Files)
	}
	if got.BaseFiles["unchanged.go"] == "" || got.HeadFiles["unchanged.go"] != got.BaseFiles["unchanged.go"] {
		t.Error("unchanged file lost from fingerprints")
	}
	if got.HeadFiles["review-only.go"] != "" || got.BaseFiles["review-only.go"] == "" || got.BaseFiles["tool"] == got.HeadFiles["tool"] {
		t.Error("deletion or mode change lost from fingerprints")
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	unchangedSHA := strings.Split(got.BaseFiles["unchanged.go"], ":")[1]
	if strings.Contains(string(calls), "/compare/") || strings.Contains(string(calls), "/blobs/"+unchangedSHA) {
		t.Errorf("should fetch exact snapshots and changed blobs only: %s", calls)
	}
}

func TestCompareRevisionsNoChangesStillIncludesFingerprints(t *testing.T) {
	dir := revisionFixture(t, map[string]revisionFixtureFile{"a": {body: "same\n"}}, nil)
	got, err := (Client{}).CompareRevisions("acme/repo", revisionBase, revisionBase)
	if err != nil {
		t.Fatal(err)
	}
	if got.Diff != "" || len(got.Files) != 0 || got.HeadFiles["a"] == "" || got.BaseFiles["a"] != got.HeadFiles["a"] {
		t.Errorf("comparison = %+v", got)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(calls), "\n") != 1 {
		t.Errorf("same snapshot should need just one tree request: %s", calls)
	}
}

func TestCompareRevisionsRejectsIncompleteHistory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response any
		want     string
	}{
		{"truncated", map[string]any{"sha": revisionBase, "tree": []any{}, "truncated": true}, "truncated"},
		{"missing entries", map[string]any{"sha": revisionBase, "truncated": false}, "incomplete"},
		{"missing truncation flag", map[string]any{"sha": revisionBase, "tree": []any{}}, "incomplete"},
		{"unknown type", map[string]any{"sha": revisionBase, "tree": []revisionEntry{{Path: "a", Type: "unknown", Mode: "100644", SHA: revisionHead}}, "truncated": false}, "invalid tree entry"},
		{"bad path", map[string]any{"sha": revisionBase, "tree": []revisionEntry{{Path: "../escape", Type: "blob", Mode: "100644", SHA: revisionHead}}, "truncated": false}, "invalid tree entry"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := revisionFixture(t, nil, nil)
			revisionWriteJSON(t, filepath.Join(dir, "tree-"+revisionBase+".json"), tc.response)
			got, err := (Client{}).CompareRevisions("acme/repo", revisionBase, revisionHead)
			if got != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %+v, %v; want complete failure containing %q", got, err, tc.want)
			}
		})
	}
}

func TestCompareRevisionsRejectsMissingAndCorruptBlobs(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint("missing=", missing), func(t *testing.T) {
			dir := revisionFixture(t, map[string]revisionFixtureFile{"a": {body: "old"}}, map[string]revisionFixtureFile{"a": {body: "new", sha: revisionHead}})
			if missing {
				if err := os.Remove(filepath.Join(dir, "blob-"+revisionHead+".json")); err != nil {
					t.Fatal(err)
				}
			}
			got, err := (Client{}).CompareRevisions("acme/repo", revisionBase, revisionHead)
			if got != nil || err == nil || !strings.Contains(err.Error(), "cannot compare") {
				t.Fatalf("got %+v, %v; want complete failure", got, err)
			}
		})
	}
}

func TestCompareRevisionsPathsAreDataAndCheckoutUntouched(t *testing.T) {
	path := "nested/quote\"\ttab\nnewline $(touch nope).go"
	revisionFixture(t, map[string]revisionFixtureFile{path: {body: "before\n"}}, map[string]revisionFixtureFile{path: {body: "after\n"}})
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, "sentinel"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	// Caller Git overrides must not redirect object writes to their repository.
	t.Setenv("GIT_DIR", filepath.Join(checkout, "objects-must-not-appear"))
	t.Setenv("GIT_WORK_TREE", checkout)
	got, err := (Client{Dir: checkout}).CompareRevisions("acme/repo", revisionBase, revisionHead)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Files) != 1 || got.Files[0].Path != path || !strings.Contains(got.Diff, "+after") {
		t.Errorf("unsafe filename mishandled: %+v", got)
	}
	entries, err := os.ReadDir(checkout)
	if err != nil || len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Errorf("checkout changed: %v, %v", entries, err)
	}
}
