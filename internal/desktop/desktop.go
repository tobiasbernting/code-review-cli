// Package desktop delivers bounded OS notifications without shell interpolation.
package desktop

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"time"
)

func Support() string {
	if runtime.GOOS == "darwin" {
		return "macOS Notification Center (permission and session dependent)"
	}
	return "unsupported on " + runtime.GOOS
}
func Notify(ctx context.Context, id, body string) error {
	if runtime.GOOS != "darwin" {
		return errors.New(Support())
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	script := `on run argv
 display notification (item 2 of argv) with title "crv attention" subtitle (item 1 of argv)
end run`
	return exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script, id, body).Run()
}
