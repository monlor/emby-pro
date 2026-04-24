package app

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/emby"
	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
	"github.com/monlor/emby-pro/internal/syncer"
)

type RescanResult struct {
	RuleName  string `json:"rule_name"`
	Scheduled bool   `json:"scheduled"`
	Pending   bool   `json:"pending"`
	NotFound  bool   `json:"not_found"`
}

type RuntimeUpdater func(cfg config.Config, runtimeClient *openlist.Client, embyClient *emby.Client)

type App struct {
	ctx        context.Context
	configPath string
	logger     *log.Logger

	mu            sync.RWMutex
	cfg           config.Config
	runtimeErr    string
	store         *index.Store
	runtimeClient *openlist.Client
	syncClient    *openlist.Client
	embyClient    *emby.Client
	updater       RuntimeUpdater
	logBuffer     *LogBuffer

	syncCancel context.CancelFunc
	syncDone   chan struct{}
	syncer     *syncer.Syncer
}

func New(ctx context.Context, configPath string, logger *log.Logger) (*App, error) {
	if logger == nil {
		logger = log.Default()
	}
	logBuffer := NewLogBuffer(300)
	logger = log.New(io.MultiWriter(logger.Writer(), logBuffer), "[emby-pro] ", log.LstdFlags)
	if _, err := config.EnsureConfigFile(configPath); err != nil {
		return nil, fmt.Errorf("ensure config file: %w", err)
	}
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config file: %w", err)
	}
	store, err := index.Open(cfg.Sync.IndexDB)
	if err != nil {
		return nil, fmt.Errorf("open index db: %w", err)
	}

	app := &App{
		ctx:        ctx,
		configPath: configPath,
		logger:     logger,
		logBuffer:  logBuffer,
		cfg:        cfg,
		store:      store,
	}
	if err := app.applyLoadedConfig(cfg); err != nil {
		app.runtimeErr = err.Error()
	}
	return app, nil
}

func (a *App) Close() error {
	a.mu.Lock()
	cancel := a.syncCancel
	done := a.syncDone
	store := a.store
	a.syncCancel = nil
	a.syncDone = nil
	a.syncer = nil
	a.store = nil
	a.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
	if store != nil {
		return store.Close()
	}
	return nil
}

func (a *App) SetRuntimeUpdater(updater RuntimeUpdater) {
	a.mu.Lock()
	a.updater = updater
	cfg := a.cfg
	runtimeClient := a.runtimeClient
	embyClient := a.embyClient
	a.mu.Unlock()

	if updater != nil && runtimeClient != nil && embyClient != nil {
		updater(cfg, runtimeClient, embyClient)
	}
}

func (a *App) CurrentConfig() config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

func (a *App) RuntimeError() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.runtimeErr
}

func (a *App) RecentLogs() []string {
	a.mu.RLock()
	buffer := a.logBuffer
	a.mu.RUnlock()
	if buffer == nil {
		return nil
	}
	return buffer.Lines()
}

func (a *App) CurrentClients() (*openlist.Client, *emby.Client) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.runtimeClient, a.embyClient
}

func (a *App) ListRuleStates() ([]index.RuleState, error) {
	a.mu.RLock()
	store := a.store
	a.mu.RUnlock()
	return store.ListRuleStates()
}

func (a *App) ListExistingTargetDirs() ([]string, error) {
	a.mu.RLock()
	baseDir := a.cfg.Sync.BaseDir
	a.mu.RUnlock()
	return listExistingTargetDirs(baseDir)
}

func (a *App) RequestFullRescan(ruleName string) (RescanResult, error) {
	a.mu.RLock()
	cfg := a.cfg
	store := a.store
	a.mu.RUnlock()

	result := RescanResult{RuleName: ruleName}
	found := false
	for _, rule := range cfg.Rules {
		if rule.Name == ruleName {
			found = true
			break
		}
	}
	if !found {
		result.NotFound = true
		return result, nil
	}

	scheduled, pending, err := store.RequestFullRescan(ruleName, now())
	if err != nil {
		return result, err
	}
	result.Scheduled = scheduled
	result.Pending = pending
	if scheduled || pending {
		if scheduled {
			a.logger.Printf("[INFO] manual rescan requested rule=%s status=scheduled", ruleName)
		} else if pending {
			a.logger.Printf("[INFO] manual rescan requested rule=%s status=pending", ruleName)
		}
		a.nudgeSyncer()
	}
	return result, nil
}

func (a *App) RequestFullRescanAll() ([]RescanResult, error) {
	a.mu.RLock()
	cfg := a.cfg
	store := a.store
	a.mu.RUnlock()

	results := make([]RescanResult, 0, len(cfg.Rules))
	for _, rule := range cfg.Rules {
		scheduled, pending, err := store.RequestFullRescan(rule.Name, now())
		if err != nil {
			return nil, err
		}
		results = append(results, RescanResult{
			RuleName:  rule.Name,
			Scheduled: scheduled,
			Pending:   pending,
		})
	}
	a.logger.Printf("[INFO] manual rescan requested scope=all rules=%d", len(results))
	a.nudgeSyncer()
	return results, nil
}

func (a *App) ForceRewriteRule(ruleName string) (RescanResult, error) {
	a.mu.RLock()
	cfg := a.cfg
	store := a.store
	s := a.syncer
	a.mu.RUnlock()

	result := RescanResult{RuleName: ruleName}
	found := false
	for _, rule := range cfg.Rules {
		if rule.Name == ruleName {
			found = true
			break
		}
	}
	if !found {
		result.NotFound = true
		return result, nil
	}
	scheduled, pending, err := store.RequestFullRescan(ruleName, now())
	if err != nil {
		return result, err
	}
	result.Scheduled = scheduled
	result.Pending = pending
	a.logger.Printf("[INFO] manual full rewrite requested rule=%s", ruleName)
	if s != nil {
		s.ForceOverwriteRule(ruleName)
	}
	a.nudgeSyncer()
	return result, nil
}

func (a *App) SaveConfig(cfg config.Config) (config.Config, error) {
	validated, err := config.Validate(cfg)
	if err != nil {
		return config.Config{}, err
	}

	runtimeClient, err := openlist.NewUnlimitedClient(validated.OpenList)
	if err != nil {
		return config.Config{}, fmt.Errorf("init runtime openlist client: %w", err)
	}
	syncClient, err := openlist.NewClient(validated.OpenList)
	if err != nil {
		return config.Config{}, fmt.Errorf("init sync openlist client: %w", err)
	}
	embyClient, err := emby.NewClient(validated.Emby)
	if err != nil {
		return config.Config{}, fmt.Errorf("init emby client: %w", err)
	}

	if err := config.SaveToFile(a.configPath, validated); err != nil {
		return config.Config{}, err
	}

	a.mu.Lock()
	a.stopSyncerLocked()
	if validated.Sync.IndexDB != a.cfg.Sync.IndexDB {
		newStore, err := index.Open(validated.Sync.IndexDB)
		if err != nil {
			a.mu.Unlock()
			return config.Config{}, fmt.Errorf("open index db: %w", err)
		}
		oldStore := a.store
		a.store = newStore
		_ = oldStore.Close()
	}
	a.cfg = validated
	a.runtimeClient = runtimeClient
	a.syncClient = syncClient
	a.embyClient = embyClient
	a.runtimeErr = ""
	updater := a.updater
	a.startSyncerLocked(validated, syncClient)
	a.mu.Unlock()

	if updater != nil {
		updater(validated, runtimeClient, embyClient)
	}
	return validated, nil
}

func listExistingTargetDirs(baseDir string) ([]string, error) {
	baseDir = filepath.Clean(filepath.FromSlash(baseDir))
	info, err := os.Stat(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	dirs := make([]string, 0, 16)
	err = filepath.WalkDir(baseDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == baseDir || !entry.IsDir() {
			return nil
		}
		relativePath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		if relativePath == "." || relativePath == "" {
			return nil
		}
		dirs = append(dirs, relativePath)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	return dirs, nil
}

func (a *App) applyLoadedConfig(cfg config.Config) error {
	runtimeClient, err := openlist.NewUnlimitedClient(cfg.OpenList)
	if err != nil {
		return fmt.Errorf("init runtime openlist client: %w", err)
	}
	embyClient, err := emby.NewClient(cfg.Emby)
	if err != nil {
		return fmt.Errorf("init emby client: %w", err)
	}

	a.mu.Lock()
	a.cfg = cfg
	a.runtimeClient = runtimeClient
	a.embyClient = embyClient
	updater := a.updater
	a.mu.Unlock()

	if updater != nil {
		updater(cfg, runtimeClient, embyClient)
	}

	validated, err := config.Validate(cfg)
	if err != nil {
		return err
	}
	syncClient, err := openlist.NewClient(validated.OpenList)
	if err != nil {
		return fmt.Errorf("init sync openlist client: %w", err)
	}

	a.mu.Lock()
	a.cfg = validated
	a.syncClient = syncClient
	a.startSyncerLocked(validated, syncClient)
	a.mu.Unlock()
	return nil
}

func (a *App) nudgeSyncer() {
	a.mu.RLock()
	s := a.syncer
	a.mu.RUnlock()
	if s != nil {
		a.logger.Printf("[INFO] syncer wake requested")
		s.Wake()
	}
}

func (a *App) stopSyncerLocked() {
	cancel := a.syncCancel
	done := a.syncDone
	a.syncCancel = nil
	a.syncDone = nil
	a.syncer = nil
	if cancel != nil {
		cancel()
	}
	if done != nil {
		a.mu.Unlock()
		<-done
		a.mu.Lock()
	}
}

func (a *App) startSyncerLocked(cfg config.Config, syncClient *openlist.Client) {
	ctx, cancel := context.WithCancel(a.ctx)
	done := make(chan struct{})
	s := syncer.New(cfg, a.store, syncClient)
	s.SetLogger(a.logger)
	a.syncer = s
	go func() {
		defer close(done)
		if err := s.Run(ctx); err != nil && ctx.Err() == nil {
			a.logger.Printf("[emby-pro] [WARN] syncer stopped: %v", err)
			a.mu.Lock()
			a.runtimeErr = err.Error()
			a.mu.Unlock()
		}
	}()
	a.syncCancel = cancel
	a.syncDone = done
}

var now = func() time.Time {
	return time.Now()
}
