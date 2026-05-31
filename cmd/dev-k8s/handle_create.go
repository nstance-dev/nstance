// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request, resource, namespace string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	metadata, ok := obj["metadata"].(map[string]interface{})
	if !ok {
		http.Error(w, "missing metadata", http.StatusBadRequest)
		return
	}

	name, ok := metadata["name"].(string)
	if !ok {
		http.Error(w, "missing metadata.name", http.StatusBadRequest)
		return
	}

	if metadata["namespace"] == nil {
		metadata["namespace"] = namespace
	}

	metadata["uid"] = fmt.Sprintf("dev-%s-%d", name, time.Now().UnixNano())
	metadata["creationTimestamp"] = time.Now().UTC().Format(time.RFC3339)
	metadata["resourceVersion"] = resourceVersion(body)
	metadata["generation"] = float64(1)

	data, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	dir := filepath.Join(s.dir, resource, namespace)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.ensureWatch(resource, namespace)

	path := filepath.Join(dir, name+".json")

	// Lock this file to prevent concurrent modifications
	lock := s.fileLock(path)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(path); err == nil {
		s.writeConflict(w, resource, name)
		return
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write(data)
}

func resourceVersion(data []byte) string {
	h := md5.Sum(data)
	return fmt.Sprintf("%x", h[:8])
}
