package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/monlor/emby-pro/internal/config"
	"github.com/monlor/emby-pro/internal/emby"
	"github.com/monlor/emby-pro/internal/index"
	"github.com/monlor/emby-pro/internal/openlist"
	"github.com/monlor/emby-pro/internal/redirect"
	"github.com/monlor/emby-pro/internal/syncer"
)

func main() {
	var once bool
	flag.BoolVar(&once, "once", false, "run a single sync cycle and exit")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if cfg.Redirect.EphemeralSecret {
		log.Printf("PLAY_TICKET_SECRET is not set; using an ephemeral in-memory secret. Existing play tickets will be invalid after restart and multi-instance deployments will not share tickets.")
	}

	store, err := index.Open(cfg.Sync.IndexDB)
	if err != nil {
		log.Fatalf("open index db: %v", err)
	}
	defer store.Close()

	client, err := openlist.NewClient(cfg.OpenList)
	if err != nil {
		log.Fatalf("init openlist client: %v", err)
	}

	s := syncer.New(cfg, store, client)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if once {
		if err := s.RunOnce(ctx); err != nil {
			log.Fatalf("run once: %v", err)
		}
		return
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- s.Run(ctx)
	}()

	embyClient, err := emby.NewClient(cfg.Emby)
	if err != nil {
		log.Fatalf("init emby client: %v", err)
	}
	redirectServer := redirect.NewServer(cfg.Redirect, client, embyClient, log.New(os.Stdout, "[emby-pro] ", log.LstdFlags), cfg.Emby.TokenCacheTTL)
	go func() {
		errCh <- redirectServer.Run(ctx)
	}()

	if err := <-errCh; err != nil {
		log.Fatalf("run service: %v", err)
	}
}
