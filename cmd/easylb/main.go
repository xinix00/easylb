package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"easylb/internal/lb"
)

func main() {
	listenAddr := flag.String("listen", ":80", "Address to listen on")
	leaderAddr := flag.String("leader", "http://127.0.0.1:8080", "Easyrun leader address")
	flag.Parse()

	log.Printf("Starting easylb on %s, watching %s", *listenAddr, *leaderAddr)

	routeTable := lb.NewRouteTable()
	watcher := lb.NewWatcher(*leaderAddr, routeTable)
	proxy := lb.NewProxy(routeTable)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start watching for route updates
	go watcher.Run(ctx)

	// Start HTTP server
	server := &http.Server{
		Addr:    *listenAddr,
		Handler: proxy,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		cancel()
		server.Close()
	}()

	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}
