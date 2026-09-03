package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func useHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	old := homeDir
	homeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { homeDir = old })
	t.Setenv("XDG_CONFIG_HOME", "")
	return home
}

func TestDirPrefersDotConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows keeps %AppData%")
	}
	home := useHome(t)
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "crv"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

func TestDirHonoursXDG(t *testing.T) {
	useHome(t)
	t.Setenv("XDG_CONFIG_HOME", "/somewhere/xdg")
	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/somewhere/xdg", "crv"); got != want {
		t.Errorf("Dir = %q, want %q", got, want)
	}
}

func TestUserPathIsInsideDir(t *testing.T) {
	useHome(t)
	dir, _ := Dir()
	path, err := UserPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, UserFile); path != want {
		t.Errorf("UserPath = %q, want %q", path, want)
	}
}

// The move must carry existing notes across, or upgrading silently loses a
// review someone had in progress.
func TestMigrateMovesLegacyDirectory(t *testing.T) {
	home := useHome(t)
	legacy := filepath.Join(home, "Legacy", "crv")
	if err := os.MkdirAll(filepath.Join(legacy, "reviews"), 0o700); err != nil {
		t.Fatal(err)
	}
	note := filepath.Join(legacy, "reviews", "abc.json")
	if err := os.WriteFile(note, []byte(`{"scope":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := userConfigDir
	userConfigDir = func() (string, error) { return filepath.Join(home, "Legacy"), nil }
	t.Cleanup(func() { userConfigDir = old })

	from, to, moved := Migrate()
	if !moved {
		t.Fatal("nothing was migrated")
	}
	if from != legacy {
		t.Errorf("moved from %q, want %q", from, legacy)
	}
	if _, err := os.Stat(filepath.Join(to, "reviews", "abc.json")); err != nil {
		t.Errorf("the note did not survive the move: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Error("the legacy directory is still there")
	}
}

// Running twice must not move anything the second time, and must never merge
// into or overwrite an existing directory.
func TestMigrateIsIdempotentAndNeverOverwrites(t *testing.T) {
	home := useHome(t)
	legacy := filepath.Join(home, "Legacy", "crv")
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	old := userConfigDir
	userConfigDir = func() (string, error) { return filepath.Join(home, "Legacy"), nil }
	t.Cleanup(func() { userConfigDir = old })

	target, _ := Dir()
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "keep.json")
	if err := os.WriteFile(marker, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, moved := Migrate(); moved {
		t.Error("migrated over an existing directory")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("existing data was disturbed: %v", err)
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Errorf("the legacy directory was removed anyway: %v", err)
	}
}

func TestMigrateDoesNothingWithoutLegacyDirectory(t *testing.T) {
	home := useHome(t)
	old := userConfigDir
	userConfigDir = func() (string, error) { return filepath.Join(home, "Legacy"), nil }
	t.Cleanup(func() { userConfigDir = old })

	if _, _, moved := Migrate(); moved {
		t.Error("migrated something that does not exist")
	}
}

// When the two paths coincide — Linux, or XDG pointing at the same place —
// there is nothing to do and certainly nothing to rename onto itself.
func TestMigrateNoopWhenPathsMatch(t *testing.T) {
	home := useHome(t)
	same := filepath.Join(home, "same")
	t.Setenv("XDG_CONFIG_HOME", same)
	old := userConfigDir
	userConfigDir = func() (string, error) { return same, nil }
	t.Cleanup(func() { userConfigDir = old })

	if err := os.MkdirAll(filepath.Join(same, "crv"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, moved := Migrate(); moved {
		t.Error("migrated a directory onto itself")
	}
}
