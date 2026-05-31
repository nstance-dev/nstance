// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"
	"time"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

func (s *Server) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	shard := r.URL.Query().Get("shard")
	allShards := r.URL.Query().Get("all_shards") == "true"

	resp, err := s.configService.Status(r.Context(), service.ConfigStatusRequest{
		Shard:     shard,
		AllShards: allShards,
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result := configStatusResponse{
		Shards: make([]shardStatus, 0, len(resp.Shards)),
	}
	for _, shard := range resp.Shards {
		status := shardStatus{
			Shard:        shard.Shard,
			Etag:         shard.Etag,
			LastModified: shard.LastModified.Format(time.RFC3339),
			Size:         shard.Size,
		}
		if shard.Error != nil {
			errStr := shard.Error.Error()
			status.Error = &errStr
		}
		result.Shards = append(result.Shards, status)
	}

	s.writeJSON(w, http.StatusOK, result)
}

type configStatusResponse struct {
	Shards []shardStatus `json:"shards"`
}

type shardStatus struct {
	Shard        string  `json:"shard"`
	Etag         string  `json:"etag,omitempty"`
	LastModified string  `json:"last_modified,omitempty"`
	Size         int64   `json:"size,omitempty"`
	Error        *string `json:"error,omitempty"`
}
