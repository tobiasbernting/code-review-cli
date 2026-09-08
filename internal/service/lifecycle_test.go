package service

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlistEscapesUntrustedPaths(t *testing.T) {
	s := Plist(`/a/"&< crv`, "/config & path/crv", "/gh <bin>", "/home/雪")
	d := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(s, "/gh <bin>") || !strings.Contains(s, "/home/雪") {
		t.Fatal(s)
	}
}

func TestLifecycleReportsUninstalledServiceWithoutError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defer func(prev bool) { supported = prev }(supported)
	supported = true
	calls := 0
	defer func(prev func(context.Context, ...string) (string, error)) { launchctl = prev }(launchctl)
	launchctl = func(_ context.Context, args ...string) (string, error) {
		calls++
		return "", errors.New("launchctl: exit status 113")
	}

	for _, tc := range []struct{ action, want string }{
		{"status", "not installed"},
		{"stop", "nothing to stop"},
	} {
		out, err := Lifecycle(context.Background(), tc.action, home)
		if err != nil || !strings.Contains(out, tc.want) {
			t.Fatalf("%s: %q %v", tc.action, out, err)
		}
	}
	if _, err := Lifecycle(context.Background(), "start", home); err == nil {
		t.Fatal("start without a plist must fail")
	}
	if calls != 0 {
		t.Fatalf("launchctl ran %d times for an uninstalled service", calls)
	}
}

func TestLifecycleSeparatesInstalledFromLoaded(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	defer func(prev bool) { supported = prev }(supported)
	supported = true
	if err := os.MkdirAll(filepath.Join(home, "Library", "LaunchAgents"), 0700); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	if err := os.WriteFile(plist, []byte(Plist("/bin/crv", home, "/usr/bin", home)), 0600); err != nil {
		t.Fatal(err)
	}
	loaded := false
	defer func(prev func(context.Context, ...string) (string, error)) { launchctl = prev }(launchctl)
	launchctl = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "print" && !loaded {
			return "", errors.New("launchctl: exit status 113")
		}
		return "state = running", nil
	}

	out, err := Lifecycle(context.Background(), "status", home)
	if err != nil || !strings.Contains(out, "not loaded") {
		t.Fatalf("unloaded status: %q %v", out, err)
	}
	if out, err = Lifecycle(context.Background(), "stop", home); err != nil || !strings.Contains(out, "not loaded") {
		t.Fatalf("stop while unloaded: %q %v", out, err)
	}
	loaded = true
	if out, err = Lifecycle(context.Background(), "status", home); err != nil || !strings.Contains(out, "loaded") {
		t.Fatalf("loaded status: %q %v", out, err)
	}
	if out, err = Lifecycle(context.Background(), "uninstall", home); err != nil {
		t.Fatalf("uninstall: %q %v", out, err)
	}
	if _, err = os.Stat(plist); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plist survived uninstall: %v", err)
	}
}
