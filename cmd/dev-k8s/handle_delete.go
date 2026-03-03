// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, resource, namespace, name string) {
	path := filepath.Join(s.dir, resource, namespace, name+".json")

	// Lock this file to prevent concurrent modifications
	lock := s.fileLock(path)
	lock.Lock()
	defer lock.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.writeNotFound(w, resource, name)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	metadata, ok := obj["metadata"].(map[string]any)
	if !ok {
		http.Error(w, "missing metadata", http.StatusInternalServerError)
		return
	}

	// Check if object has finalizers
	finalizers, _ := metadata["finalizers"].([]any)
	if len(finalizers) > 0 {
		// Object has finalizers - set deletionTimestamp instead of deleting
		if metadata["deletionTimestamp"] == nil {
			metadata["deletionTimestamp"] = time.Now().UTC().Format(time.RFC3339)
			metadata["resourceVersion"] = generateResourceVersion()

			newData, err := json.MarshalIndent(obj, "", "  ")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if err := os.WriteFile(path, newData, 0644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(newData)
			return
		}

		// Already has deletionTimestamp, return current state
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
		return
	}

	// No finalizers - delete immediately
	if err := os.Remove(path); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
