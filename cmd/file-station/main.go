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

	"github.com/CYL96/File-Station/internal/server"
)

func main() {
	syscall.Umask(0077)
	configPath := flag.String("config", "", "JSON configuration file; ignores ConfigPath, PASSWORD and UMASK (without -config, ConfigPath selects the state directory)")
	static := flag.String("static", "web/dist", "frontend assets")
	flag.Parse()
	if os.Geteuid() == 0 {
		log.Fatal("refusing to run as root; use a dedicated service user")
	}
	if _, err := os.Stat(filepath.Join(*static, "index.html")); err != nil {
		log.Fatalf("frontend assets unavailable; build web/dist or provide -static: %v", err)
	}
	config, err := server.LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	app, err := server.New(config, *static)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()
	httpServer := &http.Server{Addr: config.Address(), Handler: app, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	stop, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-stop.Done()
		app.CancelTasks()
		ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
		defer done()
		_ = httpServer.Shutdown(ctx)
	}()
	log.Printf("File Station listening on %s", config.Address())
	if err = httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
