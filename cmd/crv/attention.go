package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-isatty"
	"github.com/tobiasbernting/code-review-cli/internal/attention"
	"github.com/tobiasbernting/code-review-cli/internal/attentionstore"
	"github.com/tobiasbernting/code-review-cli/internal/config"
	"github.com/tobiasbernting/code-review-cli/internal/desktop"
	"github.com/tobiasbernting/code-review-cli/internal/ghsrc"
	"github.com/tobiasbernting/code-review-cli/internal/service"
	"github.com/tobiasbernting/code-review-cli/internal/tui"
)

func attentionEngine(ctx context.Context) (*service.Engine, config.Config, error) {
	cfg, err := config.LoadAttention()
	if err != nil {
		return nil, cfg, err
	}
	if !cfg.Attention.Enabled {
		return nil, cfg, errors.New("attention is disabled; configure [attention] in your user config")
	}
	if cfg.Host == "" {
		cfg.Host = os.Getenv("GH_HOST")
	}
	if cfg.Host == "" {
		cfg.Host = "github.com"
	}
	dir, err := config.Dir()
	if err != nil {
		return nil, cfg, err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return nil, cfg, err
	}
	s, err := attentionstore.Open(filepath.Join(dir, "attention.sqlite"))
	if err != nil {
		return nil, cfg, err
	}
	return &service.Engine{Store: s, GitHub: ghsrc.Client{Host: cfg.Host, Context: ctx, AttentionPollHint: func(d time.Duration) { s.SetMeta("poll_hint", d.String()) }}, Host: cfg.Host, Repositories: cfg.Attention.Repositories, Alerts: cfg.Attention.DesktopNotifications, Notify: desktop.Notify}, cfg, nil
}
func attentionCommand(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: crv attention sync|diagnose|history or crv service run|install|uninstall|start|stop|status")
	}
	if len(args) > 2 && !(args[0] == "attention" && args[1] == "retain" && len(args) == 3) {
		return errors.New("unexpected arguments to " + strings.Join(args[:2], " "))
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if args[0] == "service" && args[1] != "run" {
		dir, err := config.Dir()
		if err != nil {
			return err
		}
		dir, err = filepath.Abs(dir)
		if err != nil {
			return err
		}
		if args[1] == "status" {
			fmt.Println("Alert support:", desktop.Support())
			if e, _, er := attentionEngine(ctx); er == nil {
				defer e.Store.Close()
				s, er := e.Store.Snapshot(e.Repositories)
				if er != nil {
					return er
				}
				fmt.Printf("Snapshot stale: %t\n", s.LastSync.IsZero() || time.Since(s.LastSync) > 3*time.Minute)
				fmt.Printf("Identity: %s\nRepositories: %s\nLast successful sync: %s\nSync error: %s\nAlert error: %s\nAlerts: %s\n", s.Identity, strings.Join(e.Repositories, ", "), s.LastSync, s.Error, s.AlertError, desktop.Support())
			} else {
				fmt.Println("Attention configuration:", er)
			}
		}
		out, err := service.Lifecycle(ctx, args[1], dir)
		fmt.Println(out)
		return err
	}
	if args[0] == "attention" && args[1] == "diagnose" {
		cfg, err := config.LoadAttention()
		if err != nil {
			return err
		}
		if !cfg.Attention.Enabled {
			return errors.New("attention is disabled")
		}
		host := cfg.Host
		if host == "" {
			host = os.Getenv("GH_HOST")
		}
		if host == "" {
			host = "github.com"
		}
		c := ghsrc.Client{Host: host, Context: ctx}
		id, err := c.AttentionIdentity()
		if err != nil {
			return err
		}
		teams, err := c.AttentionTeams()
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		for _, repo := range cfg.Attention.Repositories {
			prs, err := c.AttentionCandidates(repo)
			if err != nil {
				return err
			}
			for _, p := range prs {
				ev, fetchErr := c.AttentionEvidence(repo, p.Number, id.Login, teams)
				if fetchErr != nil {
					ev.Problem = fetchErr.Error()
				}
				if err = enc.Encode(ev); err != nil {
					return err
				}
			}
		}
		return nil
	}
	e, cfg, err := attentionEngine(ctx)
	if err != nil {
		return err
	}
	defer e.Store.Close()
	if args[0] == "attention" && args[1] == "retain" {
		if len(args) != 3 {
			return errors.New("usage: crv attention retain owner/repo#number")
		}
		repo, num, ok := strings.Cut(args[2], "#")
		number, parseErr := strconv.Atoi(num)
		if !ok || parseErr != nil || number <= 0 || !attentionstore.InScope(e.Repositories, repo) {
			return errors.New("retain requires an in-scope owner/repo#number")
		}
		return e.Store.Retain(attention.PR{Repo: repo, Number: number, URL: "https://" + e.Host + "/" + repo + "/pull/" + num}, "Retained from cleanup history")
	}
	switch strings.Join(args, " ") {
	case "attention sync":
		return e.Sync(ctx)
	case "attention history":
		lines, err := e.Store.History()
		for _, line := range lines {
			fmt.Println(line)
		}
		return err
	case "service run":
		e.Reload = func() error {
			fresh, err := config.LoadAttention()
			if err != nil {
				return err
			}
			if !fresh.Attention.Enabled {
				return errors.New("attention disabled; worker paused")
			}
			host := fresh.Host
			if host == "" {
				host = os.Getenv("GH_HOST")
			}
			if host == "" {
				host = "github.com"
			}
			if host != e.Host {
				return errors.New("attention host changed; restart the worker")
			}
			e.Repositories = fresh.Attention.Repositories
			e.Alerts = fresh.Attention.DesktopNotifications
			return nil
		}
		interval, _ := time.ParseDuration(cfg.Attention.PollInterval)
		return e.Run(ctx, interval)
	default:
		return errors.New("unknown attention/service command")
	}
}
func runAttentionQueue(cfg config.Config, limit int) (tui.Selection, error) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	e, workerCfg, err := attentionEngine(ctx)
	if err != nil {
		return tui.Selection{}, err
	}
	defer e.Store.Close()
	th, _, err := presentation(cfg)
	if err != nil {
		return tui.Selection{}, err
	}
	if !isatty.IsTerminal(os.Stdout.Fd()) {
		syncErr := e.Sync(ctx)
		s, err := e.Store.Snapshot(e.Repositories)
		if err != nil {
			return tui.Selection{}, err
		}
		fmt.Print(tui.AttentionText(s))
		return tui.Selection{}, syncErr
	}
	model, err := tea.NewProgram(tui.NewAttentionQueue(e, ghsrc.Client{Host: workerCfg.Host, Context: ctx}, th, limit), tea.WithAltScreen()).Run()
	if err != nil {
		return tui.Selection{}, err
	}
	if m, ok := model.(tui.AttentionQueue); ok {
		return m.Selected, nil
	}
	return tui.Selection{}, nil
}
