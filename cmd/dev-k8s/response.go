// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (s *Server) writeNotFound(w http.ResponseWriter, resource, name string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":       "Status",
		"apiVersion": "v1",
		"metadata":   map[string]interface{}{},
		"status":     "Failure",
		"message":    fmt.Sprintf("%s %q not found", resource, name),
		"reason":     "NotFound",
		"code":       404,
	})
}

func (s *Server) writeConflict(w http.ResponseWriter, resource, name string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"kind":       "Status",
		"apiVersion": "v1",
		"metadata":   map[string]interface{}{},
		"status":     "Failure",
		"message":    fmt.Sprintf("%s %q already exists", resource, name),
		"reason":     "AlreadyExists",
		"code":       409,
	})
}
