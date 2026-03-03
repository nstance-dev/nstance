// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"net/http"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

func (s *Server) handleConfigRefresh(w http.ResponseWriter, r *http.Request) {
	shard := r.URL.Query().Get("shard")
	allShards := r.URL.Query().Get("all_shards") == "true"

	resp, err := s.configService.Refresh(r.Context(), service.ConfigRefreshRequest{
		Shard:     shard,
		AllShards: allShards,
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result := configRefreshResponse{
		Shards: make([]shardRefreshResult, 0, len(resp.Shards)),
	}
	for _, shard := range resp.Shards {
		res := shardRefreshResult{
			Shard:   shard.Shard,
			Updated: shard.Updated,
			Etag:    shard.Etag,
		}
		if shard.Error != nil {
			errStr := shard.Error.Error()
			res.Error = &errStr
		}
		result.Shards = append(result.Shards, res)
	}

	s.writeJSON(w, http.StatusOK, result)
}

type configRefreshResponse struct {
	Shards []shardRefreshResult `json:"shards"`
}

type shardRefreshResult struct {
	Shard   string  `json:"shard"`
	Updated bool    `json:"updated"`
	Etag    string  `json:"etag,omitempty"`
	Error   *string `json:"error,omitempty"`
}
