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

func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request, resource, namespace, name, subresource string) {
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

	var patchObj map[string]interface{}
	if err := json.Unmarshal(body, &patchObj); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if subresource == "status" {
		if status, ok := patchObj["status"]; ok {
			existingObj["status"] = status
		}
	} else {
		mergeMaps(existingObj, patchObj)
		// Increment generation when spec changes (non-status patch)
		if metadata, ok := existingObj["metadata"].(map[string]interface{}); ok {
			gen := float64(1)
			if existingGen, ok := metadata["generation"].(float64); ok {
				gen = existingGen + 1
			}
			metadata["generation"] = gen
		}
	}

	if metadata, ok := existingObj["metadata"].(map[string]interface{}); ok {
		metadata["resourceVersion"] = resourceVersion(body)
	}

	data, err := json.MarshalIndent(existingObj, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	s.ensureWatch(resource, namespace)

	if err := os.WriteFile(path, data, 0644); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func mergeMaps(dst, src map[string]interface{}) {
	for k, v := range src {
		if srcMap, ok := v.(map[string]interface{}); ok {
			if dstMap, ok := dst[k].(map[string]interface{}); ok {
				mergeMaps(dstMap, srcMap)
				continue
			}
		}
		dst[k] = v
	}
}
