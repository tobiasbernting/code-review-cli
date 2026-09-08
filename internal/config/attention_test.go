package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAttentionUserScope(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	os.MkdirAll(filepath.Join(root, "crv"), 0700)
	os.WriteFile(filepath.Join(root, "crv", UserFile), []byte("[attention]\nenabled=true\nrepositories=['one/repo']\npoll_interval='2m'\n"), 0600)
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, RepoFile), []byte("[attention]\nenabled=false\nrepositories=['evil/repo']\n"), 0600)
	cfg, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Attention.Enabled || cfg.Attention.Repositories[0] != "one/repo" {
		t.Fatal(cfg.Attention)
	}
}
func TestAttentionValidation(t *testing.T) {
	for _, a := range []Attention{{Enabled: true, PollInterval: "60s"}, {Repositories: []string{"../r/x"}, PollInterval: "60s"}, {Repositories: []string{"o/r", "O/R"}, PollInterval: "60s"}, {PollInterval: "1s"}} {
		if a.Validate() == nil {
			t.Fatal(a)
		}
	}
}
