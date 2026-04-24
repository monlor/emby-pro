package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/monlor/emby-pro/internal/app"
	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/emby"
	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
	"github.com/monlor/emby-pro/internal/redirect"
	"github.com/monlor/emby-pro/internal/syncer"
)

const defaultConfigPath = "/config/app.yml"

func main() {
	var once bool
	flag.BoolVar(&once, "once", false, "run a single sync cycle and exit")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if once {
		cfg, err := loadValidatedConfig(defaultConfigPath)
		if err != nil {
			log.Fatalf("load config: %v", err)
		}
		store, err := index.Open(cfg.Sync.IndexDB)
		if err != nil {
			log.Fatalf("open index db: %v", err)
		}
		defer store.Close()
		syncClient, err := openlist.NewClient(cfg.OpenList)
		if err != nil {
			log.Fatalf("init sync openlist client: %v", err)
		}
		s := syncer.New(cfg, store, syncClient)
		if err := s.RunOnce(ctx); err != nil {
			log.Fatalf("run once: %v", err)
		}
		return
	}

	appRuntime, err := app.New(ctx, defaultConfigPath, log.New(os.Stdout, "[emby-pro] ", log.LstdFlags))
	if err != nil {
		log.Fatalf("init app: %v", err)
	}
	defer appRuntime.Close()

	cfg := appRuntime.CurrentConfig()
	runtimeClient, embyClient := appRuntime.CurrentClients()
	redirectServer := redirect.NewServer(cfg.Redirect, runtimeClient, embyClient, redirect.AdminCallbacks{
		CurrentConfig:          appRuntime.CurrentConfig,
		RuntimeError:           appRuntime.RuntimeError,
		RecentLogs:             appRuntime.RecentLogs,
		ListRuleStates:         appRuntime.ListRuleStates,
		ListExistingTargetDirs: appRuntime.ListExistingTargetDirs,
		SaveConfig:             appRuntime.SaveConfig,
		RequestFullRescan: func(ruleName string) (redirect.AdminRescanResult, error) {
			result, err := appRuntime.RequestFullRescan(ruleName)
			return redirect.AdminRescanResult{
				RuleName:  result.RuleName,
				Scheduled: result.Scheduled,
				Pending:   result.Pending,
				NotFound:  result.NotFound,
			}, err
		},
		RequestFullRescanAll: func() ([]redirect.AdminRescanResult, error) {
			results, err := appRuntime.RequestFullRescanAll()
			if err != nil {
				return nil, err
			}
			out := make([]redirect.AdminRescanResult, 0, len(results))
			for _, result := range results {
				out = append(out, redirect.AdminRescanResult{
					RuleName:  result.RuleName,
					Scheduled: result.Scheduled,
					Pending:   result.Pending,
					NotFound:  result.NotFound,
				})
			}
			return out, nil
		},
		ForceRewriteRule: func(ruleName string) (redirect.AdminRescanResult, error) {
			result, err := appRuntime.ForceRewriteRule(ruleName)
			return redirect.AdminRescanResult{
				RuleName:  result.RuleName,
				Scheduled: result.Scheduled,
				Pending:   result.Pending,
				NotFound:  result.NotFound,
			}, err
		},
	}, log.New(os.Stdout, "[emby-pro] ", log.LstdFlags), cfg.Emby.TokenCacheTTL)
	appRuntime.SetRuntimeUpdater(func(cfg config.Config, runtimeClient *openlist.Client, embyClient *emby.Client) {
		redirectServer.UpdateRuntime(cfg.Redirect, runtimeClient, embyClient)
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- redirectServer.Run(ctx)
	}()

	if err := <-errCh; err != nil {
		log.Fatalf("run service: %v", err)
	}
}

func loadValidatedConfig(path string) (config.Config, error) {
	if _, err := config.EnsureConfigFile(path); err != nil {
		return config.Config{}, err
	}
	cfg, err := config.LoadFromFile(path)
	if err != nil {
		return config.Config{}, err
	}
	return config.Validate(cfg)
}
