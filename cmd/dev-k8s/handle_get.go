// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"os"
	"path/filepath"
)

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, resource, namespace, name, subresource string) {
	var path string
	if namespace == "" {
		// Cluster-scoped resource (e.g., nodes)
		path = filepath.Join(s.dir, resource, name+".json")
	} else {
		// Namespaced resource
		path = filepath.Join(s.dir, resource, namespace, name+".json")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			s.writeNotFound(w, resource, name)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}
