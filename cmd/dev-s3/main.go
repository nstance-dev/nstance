// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
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

	"github.com/johannesboyne/gofakes3"
	"github.com/johannesboyne/gofakes3/backend/s3afero"
	"github.com/spf13/afero"
)

func main() {
	var (
		addr   = flag.String("addr", ":8989", "Address to bind to")
		bucket = flag.String("bucket", "dev", "Default bucket to create")
		dir    = flag.String("dir", "temp/dev-s3", "Directory to store S3 files")
	)
	flag.Parse()

	// Ensure storage directory exists
	absDir, err := filepath.Abs(*dir)
	if err != nil {
		log.Fatalf("Could not resolve storage directory path: %v", err)
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		log.Fatalf("Could not create storage directory: %v", err)
	}

	// Create afero-based single-bucket S3 backend
	fs := afero.NewOsFs()
	basePathFs := afero.NewBasePathFs(fs, absDir)
	backend, err := s3afero.SingleBucket(*bucket, basePathFs, nil)
	if err != nil {
		log.Fatalf("Could not create S3 backend: %v", err)
	}

	// Create gofakes3 server
	faker := gofakes3.New(backend)

	fmt.Printf("Starting fake S3 server on %s\n", *addr)
	fmt.Printf("Storage directory: %s\n", absDir)
	fmt.Printf("Default bucket: %s\n", *bucket)
	fmt.Printf("AWS credentials: any non-empty values work\n")

	// Setup graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	server := &http.Server{
		Addr:    *addr,
		Handler: faker.Server(),
	}

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-c
	fmt.Println("\nShutting down fake S3 server...")
	if err := server.Close(); err != nil {
		log.Printf("Error closing server: %v", err)
	}
}
