// Nstance <https://nstance.dev>
// Copyright 2026 Nadrama Pty Ltd
// SPDX-License-Identifier: Apache-2.0

package topsis

import (
	"fmt"
	"math"
)

// Alternative represents a candidate solution with multiple criteria values
type Alternative struct {
	ID      string
	Metrics []float64
}

// Weights represents the importance of each criterion (higher = more important)
type Weights []float64

// BenefitFlags indicates whether higher is better (true) or lower is better (false)
type BenefitFlags []bool

// Result contains the TOPSIS score and ranking for each alternative
type Result struct {
	ID    string
	Score float64
}

// Rank performs TOPSIS ranking on alternatives based on weights and benefit flags
func Rank(alternatives []Alternative, weights Weights, benefits BenefitFlags) ([]Result, error) {
	if len(alternatives) == 0 {
		return nil, fmt.Errorf("no alternatives provided")
	}

	numCriteria := len(alternatives[0].Metrics)
	if numCriteria == 0 {
		return nil, fmt.Errorf("alternatives have no metrics")
	}

	if len(weights) != numCriteria {
		return nil, fmt.Errorf("weights length (%d) does not match metrics length (%d)", len(weights), numCriteria)
	}

	if len(benefits) != numCriteria {
		return nil, fmt.Errorf("benefits length (%d) does not match metrics length (%d)", len(benefits), numCriteria)
	}

	for _, alt := range alternatives {
		if len(alt.Metrics) != numCriteria {
			return nil, fmt.Errorf("alternative %s has %d metrics, expected %d", alt.ID, len(alt.Metrics), numCriteria)
		}
	}

	normalized := normalize(alternatives)
	weighted := applyWeights(normalized, weights)
	ideal, antiIdeal := findIdealSolutions(weighted, benefits)
	scores := calculateScores(weighted, ideal, antiIdeal)

	results := make([]Result, len(alternatives))
	for i, alt := range alternatives {
		results[i] = Result{
			ID:    alt.ID,
			Score: scores[i],
		}
	}

	return results, nil
}

func normalize(alternatives []Alternative) [][]float64 {
	numAlternatives := len(alternatives)
	numCriteria := len(alternatives[0].Metrics)

	normalized := make([][]float64, numAlternatives)
	for i := range normalized {
		normalized[i] = make([]float64, numCriteria)
	}

	for j := 0; j < numCriteria; j++ {
		sumSquares := 0.0
		for i := 0; i < numAlternatives; i++ {
			sumSquares += alternatives[i].Metrics[j] * alternatives[i].Metrics[j]
		}
		divisor := math.Sqrt(sumSquares)

		if divisor == 0 {
			divisor = 1
		}

		for i := 0; i < numAlternatives; i++ {
			normalized[i][j] = alternatives[i].Metrics[j] / divisor
		}
	}

	return normalized
}

func applyWeights(normalized [][]float64, weights Weights) [][]float64 {
	numAlternatives := len(normalized)
	numCriteria := len(normalized[0])

	weighted := make([][]float64, numAlternatives)
	for i := range weighted {
		weighted[i] = make([]float64, numCriteria)
	}

	for i := 0; i < numAlternatives; i++ {
		for j := 0; j < numCriteria; j++ {
			weighted[i][j] = normalized[i][j] * weights[j]
		}
	}

	return weighted
}

func findIdealSolutions(weighted [][]float64, benefits BenefitFlags) ([]float64, []float64) {
	numCriteria := len(weighted[0])

	ideal := make([]float64, numCriteria)
	antiIdeal := make([]float64, numCriteria)

	for j := 0; j < numCriteria; j++ {
		maxVal := math.Inf(-1)
		minVal := math.Inf(1)

		for i := 0; i < len(weighted); i++ {
			maxVal = math.Max(maxVal, weighted[i][j])
			minVal = math.Min(minVal, weighted[i][j])
		}

		if benefits[j] {
			ideal[j] = maxVal
			antiIdeal[j] = minVal
		} else {
			ideal[j] = minVal
			antiIdeal[j] = maxVal
		}
	}

	return ideal, antiIdeal
}

func calculateScores(weighted [][]float64, ideal, antiIdeal []float64) []float64 {
	numAlternatives := len(weighted)
	scores := make([]float64, numAlternatives)

	for i := 0; i < numAlternatives; i++ {
		distIdeal := 0.0
		distAntiIdeal := 0.0

		for j := 0; j < len(weighted[i]); j++ {
			diffIdeal := weighted[i][j] - ideal[j]
			diffAntiIdeal := weighted[i][j] - antiIdeal[j]

			distIdeal += diffIdeal * diffIdeal
			distAntiIdeal += diffAntiIdeal * diffAntiIdeal
		}

		distIdeal = math.Sqrt(distIdeal)
		distAntiIdeal = math.Sqrt(distAntiIdeal)

		if distIdeal+distAntiIdeal == 0 {
			scores[i] = 0
		} else {
			scores[i] = distAntiIdeal / (distIdeal + distAntiIdeal)
		}
	}

	return scores
}
