// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	var (
		addr = flag.String("addr", ":6443", "Address to bind to")
		dir  = flag.String("dir", "temp/dev-k8s", "Directory to store resources")
	)
	flag.Parse()

	absDir, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatalf("Could not resolve storage directory path: %v", err)
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		log.Fatalf("Could not create storage directory: %v", err)
	}

	server := NewServer(absDir)

	fmt.Printf("Starting fake Kubernetes API server on %s\n", *addr)
	fmt.Printf("Storage directory: %s\n", absDir)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	httpServer := &http.Server{
		Addr:    *addr,
		Handler: server,
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-c
	fmt.Println("\nShutting down fake Kubernetes API server...")
	server.Close()
	if err := httpServer.Close(); err != nil {
		log.Printf("Error closing server: %v", err)
	}
}
