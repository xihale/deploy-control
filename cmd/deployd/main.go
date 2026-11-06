// Command deployd runs the deploy-control service: GitHub webhooks,
// authenticated API and the persistent deployment queue.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"deploycontrol/internal/api"
	"deploycontrol/internal/config"
	"deploycontrol/internal/dispatch"
	"deploycontrol/internal/fetcher"
	"deploycontrol/internal/runner"
	"deploycontrol/internal/store"
	"deploycontrol/internal/webhook"
	"deploycontrol/internal/webui"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to YAML config")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	for _, dir := range []string{cfg.WorkRoot(), cfg.LogDirectory(), filepath.Dir(cfg.DBFile())} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	lock, err := os.OpenFile(cfg.DBFile()+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		log.Fatalf("instance lock: %v", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		log.Fatalf("another deployd instance is using this database: %v", err)
	}

	st, err := store.Open(cfg.DBFile())
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	f := &fetcher.Fetcher{Proxy: cfg.Fetch.Proxy, Log: func(format string, args ...any) {
		log.Printf(format, args...)
	}}
	r := &runner.Runner{Cfg: cfg, Fetch: f, LogDir: cfg.LogDirectory()}
	disp := dispatch.New(cfg, st, r)

	mux := http.NewServeMux()
	mux.Handle("/webhook/github/", &webhook.Handler{Cfg: cfg, Store: st, Disp: disp})
	apiServer := &api.Server{Cfg: cfg, Store: st, Disp: disp, LogDir: cfg.LogDirectory()}
	mux.Handle("/api/", apiServer)
	mux.Handle("/healthz", apiServer)
	webRoot := cfg.WebRoot()
	if webRoot != "" {
		ui, err := webui.New(webRoot)
		if err != nil {
			log.Fatalf("web: %v", err)
		}
		mux.Handle("/", ui)
	} else {
		mux.Handle("/", apiServer)
	}
	srv := &http.Server{
		Addr:              cfg.ListenAddr(),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	disp.Start(ctx)

	go func() {
		log.Printf("deployd listening on %s (global_concurrency=%d, sites=%d, fetch=%s, web=%s)",
			cfg.ListenAddr(), cfg.GlobalConcurrency, len(cfg.Sites), f.DescribeRemote("https://x"), webRootOrAPI(webRoot))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutdown: draining deployments")
	shCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	disp.Stop() // cancels in-flight runs; runner records "interrupted by shutdown"
	// Any job left running (e.g. hard kill window) is recovered as failed on
	// next start via dispatcher.Start → store.RecoverRunning.
	log.Printf("shutdown complete")
}

func webRootOrAPI(root string) string {
	if root == "" {
		return "disabled (API only)"
	}
	return root
}
