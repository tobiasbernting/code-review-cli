package service

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"
)

const Label = "com.crv.attention"

func xmlText(s string) string { var b bytes.Buffer; xml.EscapeText(&b, []byte(s)); return b.String() }
func Plist(executable, configDir, ghDir, home string, environment ...map[string]string) string {
	extra := ""
	for _, env := range environment {
		for _, key := range []string{"GH_HOST", "GH_CONFIG_DIR"} {
			if value := env[key]; value != "" {
				extra += "<key>" + key + "</key><string>" + xmlText(value) + "</string>"
			}
		}
	}

	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>` + Label + `</string>
<key>ProgramArguments</key><array><string>` + xmlText(executable) + `</string><string>service</string><string>run</string></array>
<key>EnvironmentVariables</key><dict><key>XDG_CONFIG_HOME</key><string>` + xmlText(filepath.Dir(configDir)) + `</string><key>HOME</key><string>` + xmlText(home) + `</string><key>PATH</key><string>` + xmlText(ghDir+":/usr/bin:/bin:/usr/sbin:/sbin") + `</string>` + extra + `</dict>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>60</integer>
<key>StandardOutPath</key><string>` + xmlText(filepath.Join(configDir, "attention-service.log")) + `</string><key>StandardErrorPath</key><string>` + xmlText(filepath.Join(configDir, "attention-service.log")) + `</string></dict></plist>`
}

// launchctl is a test seam; the real implementation shells out to launchd.
var launchctl = func(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("launchctl: %w: %s", err, out)
	}
	return string(out), nil
}

// supported is a test seam for platform gating.
var supported = runtime.GOOS == "darwin"

func Lifecycle(ctx context.Context, action, configDir string) (string, error) {
	if !supported {
		return "", errors.New("service lifecycle is supported only on macOS; use crv service run")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, "Library", "LaunchAgents", Label+".plist")
	domain := "gui/" + strconv.Itoa(os.Getuid())
	run := func(args ...string) (string, error) { return launchctl(ctx, args...) }
	installed := func() bool { _, statErr := os.Stat(path); return statErr == nil }
	switch action {
	case "install":
		cfg, err := config.LoadAttention()
		if err != nil {
			return "", err
		}
		if !cfg.Attention.Enabled {
			return "", errors.New("configure and enable attention before installing")
		}
		host := cfg.Host
		if host == "" {
			host = os.Getenv("GH_HOST")
		}
		if host == "" {
			host = "github.com"
		}
		env := map[string]string{"GH_HOST": host}
		if ghConfig := os.Getenv("GH_CONFIG_DIR"); ghConfig != "" {
			ghConfig, err = filepath.Abs(ghConfig)
			if err != nil {
				return "", err
			}
			env["GH_CONFIG_DIR"] = ghConfig
		}

		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		exe, err = filepath.EvalSymlinks(exe)
		if err != nil {
			return "", err
		}
		gh, err := exec.LookPath("gh")
		if err != nil {
			return "", err
		}
		gh, err = filepath.Abs(gh)
		if err != nil {
			return "", err
		}
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		if err = os.MkdirAll(configDir, 0700); err != nil {
			return "", err
		}
		log, logErr := os.OpenFile(filepath.Join(configDir, "attention-service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if logErr != nil {
			return "", logErr
		}
		log.Close()
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, os.ErrExist) {
			return "", errors.New("already installed at " + path + "; run crv service start, or crv service uninstall first")
		}
		if err != nil {
			return "", err
		}
		_, err = f.WriteString(Plist(exe, configDir, filepath.Dir(gh), home, env))
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		return "installed " + path + "; run crv service start", nil
	case "start":
		if !installed() {
			return "", errors.New("not installed; run crv service install first")
		}
		return run("bootstrap", domain, path)
	case "stop":
		if !installed() {
			return "not installed; nothing to stop", nil
		}
		if _, err = run("print", domain+"/"+Label); err != nil {
			return "service is not loaded", nil
		}
		return run("bootout", domain+"/"+Label)
	case "status":
		if !installed() {
			return "Service: not installed; run crv service install", nil
		}
		out, err := run("print", domain+"/"+Label)
		if err != nil {
			return "Service: installed at " + path + " but not loaded; run crv service start", nil
		}
		return "Service: loaded\n" + out, nil
	case "uninstall":
		if _, err = run("print", domain+"/"+Label); err == nil {
			if _, err = run("bootout", domain+"/"+Label); err != nil {
				return "", err
			}
		}
		err = os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		return "service removed; tasks, configuration and history preserved", err
	default:
		return "", fmt.Errorf("unknown service action %q", action)
	}
}
