package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dagger/dagger/dev/alpine/httpcacheproxy"
)

func init() {
	log.SetPrefix("🤡YOLOOOO")
}

func main() {
	log.Println("YOLOOOOOOO", os.Args)
	addr := os.Args[1]
	cacheDir := os.Args[2]
	shutdownCh := make(chan error)
	server, err := httpcacheproxy.New(addr, cacheDir, shutdownCh)
	if err != nil {
		log.Fatalf("could not instantiate http cache proxy server: %v", err)
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http cache proxy server returned: %v", err)
		}
	}()

	log.Println("Dagger HTTP Cache Proxy running on", addr)
	<-stop

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Attempt to gracefully shutdown the server
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Server forced to shutdown: %v\n", err)
	} else {
		log.Println("Server exited properly.")
	}
	err = <-shutdownCh
	log.Println("received shutdownCh", err)
}
