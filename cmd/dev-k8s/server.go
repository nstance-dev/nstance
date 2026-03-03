// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/sha256"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

func parsePath(path string) (resource, namespace, name, subresource string) {
	path = strings.TrimPrefix(path, "/")
	parts := strings.Split(path, "/")

	// /api/v1/namespaces/{ns}/secrets/{name}
	// /api/v1/namespaces/{ns}/configmaps/{name}
	// /apis/infrastructure.cluster.x-k8s.io/v1beta1/namespaces/{ns}/nstancemachines/{name}
	// /apis/cluster.x-k8s.io/v1beta2/namespaces/{ns}/machines/{name}

	if len(parts) < 2 {
		return "", "", "", ""
	}

	var idx int
	switch parts[0] {
	case "api":
		idx = 2
	case "apis":
		idx = 3
	default:
		return "", "", "", ""
	}

	if len(parts) <= idx {
		return "", "", "", ""
	}

	if parts[idx] == "namespaces" && len(parts) > idx+2 {
		namespace = parts[idx+1]
		resource = parts[idx+2]
		if len(parts) > idx+3 {
			name = parts[idx+3]
		}
		if len(parts) > idx+4 {
			subresource = parts[idx+4]
		}
	} else {
		resource = parts[idx]
		if len(parts) > idx+1 {
			name = parts[idx+1]
		}
	}

	return resource, namespace, name, subresource
}

// Server is the fake Kubernetes API server.
type Server struct {
	dir     string
	watcher *fsnotify.Watcher
	watches map[string][]chan WatchEvent
	// fileCache tracks content hash and generation for auto-increment logic
	fileCache map[string]*fileCacheEntry
	mu        sync.RWMutex
	// fileLocks provides per-file locking to prevent concurrent writes
	fileLocks   map[string]*sync.Mutex
	fileLocksMu sync.Mutex
}

// fileCacheEntry tracks file state for generation auto-increment
type fileCacheEntry struct {
	specHash   [32]byte
	generation int64
}

// WatchEvent represents a Kubernetes watch event.
type WatchEvent struct {
	Type   string          `json:"type"`
	Object json.RawMessage `json:"object"`
}

// NewServer creates a new fake Kubernetes API server.
func NewServer(dir string) *Server {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("Could not create file watcher: %v", err)
	}

	s := &Server{
		dir:       dir,
		watcher:   watcher,
		watches:   make(map[string][]chan WatchEvent),
		fileCache: make(map[string]*fileCacheEntry),
		fileLocks: make(map[string]*sync.Mutex),
	}

	s.initFileCache()
	go s.watchFiles()

	return s
}

// initFileCache scans existing files and populates the cache on startup.
func (s *Server) initFileCache() {
	// Walk the directory and cache all existing .json files
	// This is done synchronously before starting the watcher
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}

	for _, resourceDir := range entries {
		if !resourceDir.IsDir() {
			continue
		}
		resourcePath := filepath.Join(s.dir, resourceDir.Name())
		namespaces, err := os.ReadDir(resourcePath)
		if err != nil {
			continue
		}
		for _, nsDir := range namespaces {
			if !nsDir.IsDir() {
				continue
			}
			nsPath := filepath.Join(resourcePath, nsDir.Name())
			files, err := os.ReadDir(nsPath)
			if err != nil {
				continue
			}
			for _, file := range files {
				if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
					continue
				}
				filePath := filepath.Join(nsPath, file.Name())
				s.cacheFile(filePath)
			}
		}
	}
}

// cacheFile reads a file and caches its spec hash and generation.
func (s *Server) cacheFile(filePath string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return
	}

	generation := int64(0)
	if meta, ok := obj["metadata"].(map[string]any); ok {
		if gen, ok := meta["generation"].(float64); ok {
			generation = int64(gen)
		}
	}

	s.mu.Lock()
	s.fileCache[filePath] = &fileCacheEntry{
		specHash:   hashSpec(obj),
		generation: generation,
	}
	s.mu.Unlock()
}

// fileLock returns a lock for the given file path, creating one if needed.
func (s *Server) fileLock(path string) *sync.Mutex {
	s.fileLocksMu.Lock()
	defer s.fileLocksMu.Unlock()
	if s.fileLocks[path] == nil {
		s.fileLocks[path] = &sync.Mutex{}
	}
	return s.fileLocks[path]
}

// hashSpec returns a hash of only the spec field
func hashSpec(obj map[string]any) [32]byte {
	spec, ok := obj["spec"]
	if !ok {
		return [32]byte{}
	}
	specBytes, err := json.Marshal(spec)
	if err != nil {
		return [32]byte{}
	}
	return sha256.Sum256(specBytes)
}

// Close shuts down the server.
func (s *Server) Close() {
	_ = s.watcher.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, chans := range s.watches {
		for _, ch := range chans {
			close(ch)
		}
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// Log all requests except health checks
	if path != "/healthz" && path != "/readyz" && path != "/livez" {
		if r.URL.RawQuery != "" {
			log.Printf("[dev-k8s] %s %s?%s", r.Method, path, r.URL.RawQuery)
		} else {
			log.Printf("[dev-k8s] %s %s", r.Method, path)
		}
	}

	// Health endpoints
	if path == "/healthz" || path == "/readyz" || path == "/livez" {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// API discovery
	if path == "/api" || path == "/apis" || path == "/api/v1" ||
		(strings.HasPrefix(path, "/apis/") && strings.Count(path, "/") <= 3) {
		s.handleDiscovery(w, r)
		return
	}

	// Parse resource path
	resource, namespace, name, subresource := parsePath(path)
	if resource == "" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	isWatch := r.URL.Query().Get("watch") == "true" || r.URL.Query().Get("watch") == "1"

	switch r.Method {
	case http.MethodGet:
		if isWatch {
			s.handleWatch(w, r, resource, namespace)
		} else if name == "" {
			s.handleList(w, r, resource, namespace)
		} else {
			s.handleGet(w, r, resource, namespace, name, subresource)
		}
	case http.MethodPost:
		if resource == "serviceaccounts" && subresource == "token" {
			s.handleServiceAccountToken(w, r, namespace, name)
		} else {
			s.handleCreate(w, r, resource, namespace)
		}
	case http.MethodPut:
		s.handleUpdate(w, r, resource, namespace, name, subresource)
	case http.MethodPatch:
		s.handlePatch(w, r, resource, namespace, name, subresource)
	case http.MethodDelete:
		s.handleDelete(w, r, resource, namespace, name)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}
