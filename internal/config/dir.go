package config

import (
	"os"
	"path/filepath"
	"runtime"
)

// homeDir is a variable so tests can redirect it.
var homeDir = os.UserHomeDir

// Dir is where krv keeps its configuration, saved reviews and caches.
//
// XDG semantics are used on Unix rather than os.UserConfigDir, which returns
// ~/Library/Application Support on macOS — a path that is awkward to type,
// awkward to quote, and not where anyone looks for a CLI's dotfiles. Windows
// keeps %AppData%, where its own conventions apply.
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "krv"), nil
	}
	if runtime.GOOS == "windows" {
		base, err := userConfigDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(base, "krv"), nil
	}
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "krv"), nil
}

// legacyName is what krv was called before v2.
const legacyName = "crv"

// LegacyDirs lists where earlier versions stored everything, most recent
// first: Dir's location under the old name, then os.UserConfigDir under the
// old name, where versions before the XDG switch kept it on macOS.
func LegacyDirs() ([]string, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	base, err := userConfigDir()
	if err != nil {
		return nil, err
	}
	return []string{
		filepath.Join(filepath.Dir(dir), legacyName),
		filepath.Join(base, legacyName),
	}, nil
}

// Migrate moves the most recent existing legacy directory to Dir once, so
// saved notes are not silently orphaned by the move. It does nothing when
// there is nothing to move or when the destination already exists — never
// merging, never overwriting. Older legacy directories are left in place.
//
// It returns the paths involved when a move happened.
func Migrate() (from, to string, moved bool) {
	to, err := Dir()
	if err != nil {
		return "", "", false
	}
	if _, err := os.Stat(to); err == nil {
		return "", "", false
	}
	candidates, err := LegacyDirs()
	if err != nil {
		return "", "", false
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			from = c
			break
		}
	}
	if from == "" {
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
