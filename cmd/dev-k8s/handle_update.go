// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request, resource, namespace, name, subresource string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	path := filepath.Join(s.dir, resource, namespace, name+".json")

	// Lock this file to prevent concurrent modifications
	lock := s.fileLock(path)
	lock.Lock()
	defer lock.Unlock()

	var newObj map[string]interface{}
	if err := json.Unmarshal(body, &newObj); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	existingData, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.writeNotFound(w, resource, name)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var existingObj map[string]interface{}
	if err := json.Unmarshal(existingData, &existingObj); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if subresource == "status" {
		existingObj["status"] = newObj["status"]
		if metadata, ok := existingObj["metadata"].(map[string]interface{}); ok {
			metadata["resourceVersion"] = resourceVersion(body)
		}
		newObj = existingObj
	} else {
		// Increment generation when spec changes (non-status update)
		if metadata, ok := newObj["metadata"].(map[string]interface{}); ok {
			metadata["resourceVersion"] = resourceVersion(body)
			// Get existing generation and increment it
			if existingMeta, ok := existingObj["metadata"].(map[string]interface{}); ok {
				gen := float64(1)
				if existingGen, ok := existingMeta["generation"].(float64); ok {
					gen = existingGen + 1
				}
				metadata["generation"] = gen
			}
		}
	}

	data, err := json.MarshalIndent(newObj, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.ensureWatch(resource, namespace)

	// Check if object should be deleted (has deletionTimestamp and no finalizers)
	if metadata, ok := newObj["metadata"].(map[string]interface{}); ok {
		if metadata["deletionTimestamp"] != nil {
			finalizers, _ := metadata["finalizers"].([]interface{})
			if len(finalizers) == 0 {
				// No more finalizers - delete the object
				if err := os.Remove(path); err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(data)
				return
			}
		}
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
