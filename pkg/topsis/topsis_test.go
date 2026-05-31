// Nstance <https://nstance.dev>
// Copyright The Nstance Authors
// SPDX-License-Identifier: Apache-2.0

package topsis

import (
	"testing"
)

func TestRank(t *testing.T) {
	tests := []struct {
		name         string
		alternatives []Alternative
		weights      Weights
		benefits     BenefitFlags
		wantErr      bool
		expectBest   string
	}{
		{
			name: "simple cpu/memory ranking",
			alternatives: []Alternative{
				{ID: "node1", Metrics: []float64{80, 60}},
				{ID: "node2", Metrics: []float64{40, 20}},
				{ID: "node3", Metrics: []float64{60, 80}},
			},
			weights:    Weights{1.0, 1.0},
			benefits:   BenefitFlags{true, true},
			wantErr:    false,
			expectBest: "node3",
		},
		{
			name: "prefer low usage (cost minimization)",
			alternatives: []Alternative{
				{ID: "node1", Metrics: []float64{90, 80}},
				{ID: "node2", Metrics: []float64{10, 20}},
				{ID: "node3", Metrics: []float64{50, 50}},
			},
			weights:    Weights{1.0, 1.0},
			benefits:   BenefitFlags{false, false},
			wantErr:    false,
			expectBest: "node2",
		},
		{
			name: "weighted criteria",
			alternatives: []Alternative{
				{ID: "node1", Metrics: []float64{100, 20}},
				{ID: "node2", Metrics: []float64{50, 100}},
			},
			weights:    Weights{3.0, 1.0},
			benefits:   BenefitFlags{true, true},
			wantErr:    false,
			expectBest: "node1",
		},
		{
			name:         "no alternatives",
			alternatives: []Alternative{},
			weights:      Weights{1.0},
			benefits:     BenefitFlags{true},
			wantErr:      true,
		},
		{
			name: "mismatched weights",
			alternatives: []Alternative{
				{ID: "node1", Metrics: []float64{100, 20}},
			},
			weights:  Weights{1.0},
			benefits: BenefitFlags{true, true},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := Rank(tt.alternatives, tt.weights, tt.benefits)
			if (err != nil) != tt.wantErr {
				t.Errorf("Rank() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil {
				return
			}

			if len(results) != len(tt.alternatives) {
				t.Errorf("expected %d results, got %d", len(tt.alternatives), len(results))
				return
			}

			bestScore := -1.0
			bestID := ""
			for _, r := range results {
				if r.Score > bestScore {
					bestScore = r.Score
					bestID = r.ID
				}
			}

			if tt.expectBest != "" && bestID != tt.expectBest {
				t.Errorf("expected best = %s, got %s (score %.4f)", tt.expectBest, bestID, bestScore)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	alternatives := []Alternative{
		{ID: "a", Metrics: []float64{3, 4}},
		{ID: "b", Metrics: []float64{0, 0}},
	}

	normalized := normalize(alternatives)

	if len(normalized) != 2 {
		t.Errorf("expected 2 normalized alternatives, got %d", len(normalized))
	}

	if normalized[1][0] != 0 || normalized[1][1] != 0 {
		t.Errorf("expected zero values to remain zero after normalization")
	}
}

func TestCalculateScores(t *testing.T) {
	weighted := [][]float64{
		{1.0, 1.0},
		{0.0, 0.0},
	}
	ideal := []float64{1.0, 1.0}
	antiIdeal := []float64{0.0, 0.0}

	scores := calculateScores(weighted, ideal, antiIdeal)

	if scores[0] != 1.0 {
		t.Errorf("expected ideal alternative to have score 1.0, got %.4f", scores[0])
	}

	if scores[1] != 0.0 {
		t.Errorf("expected anti-ideal alternative to have score 0.0, got %.4f", scores[1])
	}
}
