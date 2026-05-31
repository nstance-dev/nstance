// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"net/http"

	"github.com/nstance-dev/nstance/internal/admin/service"
)

func (s *Server) handleGroupScale(w http.ResponseWriter, r *http.Request) {
	var req groupScaleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Group == "" {
		s.writeError(w, http.StatusBadRequest, "group is required")
		return
	}
	if req.Size < 0 {
		s.writeError(w, http.StatusBadRequest, "size must be non-negative")
		return
	}

	shard := r.URL.Query().Get("shard")
	allShards := r.URL.Query().Get("all_shards") == "true"

	if shard == "" && !allShards {
		s.writeError(w, http.StatusBadRequest, "must specify shard or all_shards=true")
		return
	}
	if shard != "" && allShards {
		s.writeError(w, http.StatusBadRequest, "shard and all_shards are mutually exclusive")
		return
	}

	scaleReq := service.GroupScaleRequest{
		Shard: shard,
		Group: req.Group,
		Size:  req.Size,
	}
	if allShards {
		scaleReq.Shard = ""
	}

	resp, err := s.groupService.Scale(r.Context(), scaleReq)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	result := groupScaleResponse{
		Results: make([]groupScaleResult, 0, len(resp.Results)),
	}
	for _, res := range resp.Results {
		item := groupScaleResult{
			Shard: res.Shard,
			Group: res.Group,
			Size:  res.Size,
		}
		if res.Error != nil {
			errStr := res.Error.Error()
			item.Error = &errStr
		}
		result.Results = append(result.Results, item)
	}

	s.writeJSON(w, http.StatusOK, result)
}

type groupScaleRequest struct {
	Group string `json:"group"`
	Size  int32  `json:"size"`
}

type groupScaleResponse struct {
	Results []groupScaleResult `json:"results"`
}

type groupScaleResult struct {
	Shard string  `json:"shard"`
	Group string  `json:"group"`
	Size  int32   `json:"size"`
	Error *string `json:"error,omitempty"`
}
