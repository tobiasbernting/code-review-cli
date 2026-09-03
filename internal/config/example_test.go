package config

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateExample = flag.Bool("update-example", false, "rewrite config.example.toml")

// exampleFile is the copy checked into the repository, for people reading it
// on GitHub rather than running --init-config.
const exampleFile = "../../config.example.toml"

// The example in the repository and the template the binary writes must be
// the same file. Two copies of documentation drift, and the one on GitHub is
// the one people read first.
func TestExampleMatchesTemplate(t *testing.T) {
	if *updateExample {
		if err := os.WriteFile(exampleFile, []byte(Template()), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Log("rewrote " + filepath.Clean(exampleFile))
		return
	}

	got, err := os.ReadFile(exampleFile)
	if err != nil {
		t.Fatalf("%v\n\nrun: go test ./internal/config -update-example", err)
	}
	if string(got) != Template() {
		t.Errorf("config.example.toml is out of date — run: go test ./internal/config -update-example")
	}
}
