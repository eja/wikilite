// Copyright (C) by Ubaldo Porcheddu <ubaldo@eja.it>

package main

import (
	"fmt"
	"math"
	"math/bits"
)

func EuclideanDistance(a, b []float32) (float32, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("vectors must have the same length")
	}

	var sum float32
	for i := range a {
		diff := a[i] - b[i]
		sum += diff * diff
	}

	return float32(math.Sqrt(float64(sum))), nil
}

func HammingDistance(a, b []byte) (float32, error) {
	if len(a) != len(b) {
		return 0, fmt.Errorf("bit arrays must have the same length")
	}

	diffCount := 0
	for i := range a {
		diffBits := a[i] ^ b[i]
		diffCount += bits.OnesCount8(diffBits)
	}

	return float32(diffCount), nil
}

func LevenshteinDistance(s1, s2 string) int {
	r1, r2 := []rune(s1), []rune(s2)
	len1, len2 := len(r1), len(r2)

	dp := make([][]int, len1+1)
	for i := range dp {
		dp[i] = make([]int, len2+1)
	}

	for i := 0; i <= len1; i++ {
		dp[i][0] = i
	}
	for j := 0; j <= len2; j++ {
		dp[0][j] = j
	}

	for i := 1; i <= len1; i++ {
		for j := 1; j <= len2; j++ {
			if r1[i-1] == r2[j-1] {
				dp[i][j] = dp[i-1][j-1]
			} else {
				dp[i][j] = min(
					// Deletion.
					// Insertion.
					dp[i-1][j-1]+1,
					// Substitution.
					min(

						dp[i][j-1]+1,

						dp[i-1][j]+1))
			}
		}
	}

	return dp[len1][len2]
}
