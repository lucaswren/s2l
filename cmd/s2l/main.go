package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lucaswren/s2l/internal/api"
	"github.com/lucaswren/s2l/internal/config"
	"github.com/lucaswren/s2l/internal/l2tp"
	"github.com/lucaswren/s2l/internal/network"
	"github.com/lucaswren/s2l/internal/service"
	"github.com/lucaswren/s2l/internal/singbox"
	"github.com/lucaswren/s2l/internal/store"
	"github.com/lucaswren/s2l/web"
)

func main() {
	cfgPath := flag.String("config", "", "path to config JSON (optional)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	st, err := store.New(cfg.StatePath())
	if err != nil {
		log.Fatalf("store: %v", err)
	}

	sb := singbox.NewManager(cfg.SingboxBin)
	netCtrl := network.NewController()
	l2tpMgr := l2tp.NewManager(cfg.ChapSecrets, cfg.Xl2tpdConf, cfg.PPPOptions)
	svc := service.NewMappingService(st, sb, netCtrl, l2tpMgr)

	// 先修复可能被 # 注释写坏的 xl2tpd.conf，再恢复隧道
	svc.EnsureL2TP()

	if cfg.ShouldAutoRestore() {
		go func() {
			// 稍等 TUN/网络栈就绪
			time.Sleep(500 * time.Millisecond)
			svc.RestoreRunning()
		}()
	}

	webFS, err := web.FS()
	if err != nil {
		log.Fatalf("embed web: %v", err)
	}

	srv := api.NewServer(st, svc, webFS, cfg.AdminUser, cfg.AdminPass)
	srv.ConfigureSettings(*cfgPath)
	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		log.Printf("s2l listening on %s", cfg.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	<-ch
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	svc.Shutdown()
	log.Println("bye")
}
