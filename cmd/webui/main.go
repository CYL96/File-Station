package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/nixos-webui/internal/server"
)

func main() {
	configPath := flag.String("config", "config.json", "JSON configuration")
	static := flag.String("static", "web/dist", "frontend assets")
	flag.Parse()
	if os.Geteuid() == 0 {
		log.Fatal("refusing to run as root; use a dedicated service user")
	}
	if _, err := os.Stat(*static + "/index.html"); err != nil {
		log.Fatalf("frontend assets unavailable; build web/dist or provide -static: %v", err)
	}
	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	var config server.Config
	if err = json.Unmarshal(data, &config); err != nil {
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
