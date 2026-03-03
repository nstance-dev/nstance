// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fsnotify/fsnotify"
)

func (s *Server) watchFiles() {
	for {
		select {
		case event, ok := <-s.watcher.Events:
			if !ok {
				return
			}
			log.Printf("[dev-k8s] fsnotify event: %s %s", event.Op, event.Name)
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
				s.handleFileEvent(event)
			}
		case err, ok := <-s.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("File watcher error: %v", err)
		}
	}
}

func (s *Server) handleFileEvent(event fsnotify.Event) {
	// Check if it's a directory creation - we need to watch new namespace dirs
	if event.Op&fsnotify.Create != 0 && !strings.HasSuffix(event.Name, ".json") {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			if err := s.watcher.Add(event.Name); err != nil {
				log.Printf("[dev-k8s] Failed to add watch for new dir %s: %v", event.Name, err)
			} else {
				log.Printf("[dev-k8s] Added watch for new dir %s", event.Name)
			}
		}
		return
	}

	if !strings.HasSuffix(event.Name, ".json") {
		return
	}

	relPath, err := filepath.Rel(s.dir, event.Name)
	if err != nil {
		return
	}

	parts := strings.Split(relPath, string(filepath.Separator))
	if len(parts) < 3 {
		return
	}

	kind := parts[0]
	namespace := parts[1]

	var eventType string
	var data []byte

	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		// On macOS, Finder deletes send RENAME not REMOVE
		// Check if file actually exists - if not, treat as delete
		if _, err := os.Stat(event.Name); err == nil {
			// File still exists, this is a real rename (move), ignore
			return
		}
		eventType = "DELETED"
		name := strings.TrimSuffix(parts[2], ".json")
		data = []byte(fmt.Sprintf(`{"apiVersion":"%s","kind":"%s","metadata":{"name":"%s","namespace":"%s"}}`,
			resourceToAPIVersion(kind), singularToKind(kind), name, namespace))
		// Remove from cache
		s.mu.Lock()
		delete(s.fileCache, event.Name)
		s.mu.Unlock()
		log.Printf("[dev-k8s] File deleted: %s", event.Name)
	} else {
		if event.Op&fsnotify.Create != 0 {
			eventType = "ADDED"
		} else {
			eventType = "MODIFIED"
		}
		data, err = os.ReadFile(event.Name)
		if err != nil {
			return
		}

		// Check if we need to auto-increment generation
		data = s.autoUpdateGeneration(event.Name, data)
	}

	watchEvent := WatchEvent{Type: eventType, Object: data}

	s.mu.RLock()
	namespacedKey := fmt.Sprintf("%s/%s", kind, namespace)
	clusterWideKey := fmt.Sprintf("%s/", kind)
	chans := append(s.watches[namespacedKey], s.watches[clusterWideKey]...)
	s.mu.RUnlock()

	for _, ch := range chans {
		select {
		case ch <- watchEvent:
		default:
		}
	}
}

// autoUpdateGeneration increments generation when spec changes,
// unless the user already bumped it manually.
func (s *Server) autoUpdateGeneration(filePath string, data []byte) []byte {
	// Lock this file to prevent concurrent modifications
	lock := s.fileLock(filePath)
	lock.Lock()
	defer lock.Unlock()

	// Parse the current object
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return data
	}

	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return data
	}

	// Hash only the spec field
	specHash := hashSpec(obj)

	currentGen := int64(0)
	if gen, ok := meta["generation"].(float64); ok {
		currentGen = int64(gen)
	}

	s.mu.RLock()
	cached := s.fileCache[filePath]
	s.mu.RUnlock()

	// First time seeing this file - just cache it
	if cached == nil {
		s.mu.Lock()
		s.fileCache[filePath] = &fileCacheEntry{
			specHash:   specHash,
			generation: currentGen,
		}
		s.mu.Unlock()
		return data
	}

	// Spec unchanged - but we still need to update cached generation if it changed
	// (e.g., when a non-spec PUT like adding annotations increments generation)
	if specHash == cached.specHash {
		if currentGen != cached.generation {
			s.mu.Lock()
			s.fileCache[filePath] = &fileCacheEntry{
				specHash:   specHash,
				generation: currentGen,
			}
			s.mu.Unlock()
		}
		return data
	}

	// Spec changed - check if user already bumped generation
	if currentGen > cached.generation {
		// User bumped it - just update cache
		s.mu.Lock()
		s.fileCache[filePath] = &fileCacheEntry{
			specHash:   specHash,
			generation: currentGen,
		}
		s.mu.Unlock()
		return data
	}

	// Spec changed but generation wasn't bumped - auto-increment
	newGen := cached.generation + 1
	meta["generation"] = newGen

	// Also update resourceVersion
	meta["resourceVersion"] = generateResourceVersion()

	// Marshal the updated object
	newData, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return data
	}

	// Update cache BEFORE writing (to avoid re-triggering ourselves)
	s.mu.Lock()
	s.fileCache[filePath] = &fileCacheEntry{
		specHash:   specHash,
		generation: newGen,
	}
	s.mu.Unlock()

	// Write the updated file
	if err := os.WriteFile(filePath, newData, 0644); err != nil {
		log.Printf("Failed to write updated file %s: %v", filePath, err)
		return data
	}

	log.Printf("[dev-k8s] Auto-incremented generation for %s: %d -> %d", filePath, cached.generation, newGen)
	return newData
}

func generateResourceVersion() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request, resource, namespace string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch := make(chan WatchEvent, 100)
	watchKey := fmt.Sprintf("%s/%s", resource, namespace)

	s.mu.Lock()
	s.watches[watchKey] = append(s.watches[watchKey], ch)
	s.mu.Unlock()

	s.ensureWatch(resource, namespace)

	defer func() {
		s.mu.Lock()
		chans := s.watches[watchKey]
		for i, c := range chans {
			if c == ch {
				s.watches[watchKey] = append(chans[:i], chans[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
		close(ch)
	}()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = w.Write(data)
			_, _ = w.Write([]byte("\n"))
			flusher.Flush()
		}
	}
}

func (s *Server) ensureWatch(resource, namespace string) {
	resourceDir := filepath.Join(s.dir, resource)
	_ = os.MkdirAll(resourceDir, 0755)

	if namespace == "" {
		if err := s.watcher.Add(resourceDir); err != nil {
			log.Printf("[dev-k8s] Failed to add watch for resource dir %s: %v", resourceDir, err)
		} else {
			log.Printf("[dev-k8s] Added watch for resource dir %s", resourceDir)
		}
		nsDirs, err := os.ReadDir(resourceDir)
		if err == nil {
			for _, nsDir := range nsDirs {
				if nsDir.IsDir() {
					nsPath := filepath.Join(resourceDir, nsDir.Name())
					if err := s.watcher.Add(nsPath); err != nil {
						log.Printf("[dev-k8s] Failed to add watch for ns dir %s: %v", nsPath, err)
					} else {
						log.Printf("[dev-k8s] Added watch for ns dir %s", nsPath)
					}
				}
			}
		}
	} else {
		dir := filepath.Join(resourceDir, namespace)
		_ = os.MkdirAll(dir, 0755)
		if err := s.watcher.Add(dir); err != nil {
			log.Printf("[dev-k8s] Failed to add watch for %s: %v", dir, err)
		}

		// Also add to watcher if there's a cluster-wide watch that started before this dir existed
		s.mu.RLock()
		clusterWideKey := fmt.Sprintf("%s/", resource)
		hasClusterWatch := len(s.watches[clusterWideKey]) > 0
		s.mu.RUnlock()
		if hasClusterWatch {
			_ = s.watcher.Add(dir)
		}
	}
}
