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
	agentAddr := flag.String("agent", "http://127.0.0.1:8080", "Local easyrun agent address")
	tagFilter := flag.String("tag", "", "Only route jobs with this tag (e.g., lb:easyflor)")
	flag.Parse()

	log.Printf("Starting easylb on %s, agent=%s, tag=%q", *listenAddr, *agentAddr, *tagFilter)

	routeTable := lb.NewRouteTable()
	watcher := lb.NewWatcher(*agentAddr, routeTable, *tagFilter)
	proxy := lb.NewProxy(routeTable)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go watcher.Run(ctx)

	server := &http.Server{
		Addr:    *listenAddr,
		Handler: proxy,
	}

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
