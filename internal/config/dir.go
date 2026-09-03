package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// homeDir is a variable so tests can redirect it.
var homeDir = os.UserHomeDir

// Dir is where crv keeps its configuration, saved reviews and caches.
//
// XDG semantics are used on Unix rather than os.UserConfigDir, which returns
// ~/Library/Application Support on macOS — a path that is awkward to type,
// awkward to quote, and not where anyone looks for a CLI's dotfiles. Windows
// keeps %AppData%, where its own conventions apply.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "crv"), nil
	}
	if runtime.GOOS == "windows" {
		base, err := userConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "crv"), nil
	}
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "crv"), nil
}

// LegacyDir is where earlier versions stored everything: os.UserConfigDir,
// which differs from Dir on macOS.
func LegacyDir() (string, error) {
	base, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "crv"), nil
}

// Migrate moves an existing legacy directory to Dir once, so saved notes are
// not silently orphaned by the move. It does nothing when the two paths are
// the same, when there is nothing to move, or when the destination already
// exists — never merging, never overwriting.
//
// It returns the paths involved when a move happened.
func Migrate() (from, to string, moved bool) {
	to, err := Dir()
	if err != nil {
		return "", "", false
	}
	from, err = LegacyDir()
	if err != nil || from == to {
		return "", "", false
	}
	if _, err := os.Stat(to); err == nil {
		return "", "", false
	}
	if info, err := os.Stat(from); err != nil || !info.IsDir() {
		return "", "", false
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		return "", "", false
	}
	// A failed rename leaves the legacy directory untouched; the caller then
	// simply starts with an empty new one rather than losing anything.
	if err := os.Rename(from, to); err != nil {
		return "", "", false
	}
	return from, to, true
}
