package db

import "math"

// allocHintSum returns a safe preallocation hint for slices and maps.
// If the combined size would overflow int, it falls back to zero capacity.
func allocHintSum(values ...int) int {
	total := 0
	for _, value := range values {
		if value <= 0 {
			if value < 0 {
				return 0
			}
			continue
		}
		if total > math.MaxInt-value {
			return 0
		}
		total += value
	}
	return total
}

// allocHintMul returns a safe preallocation hint for repeated expansions.
// If the multiplication would overflow int, it falls back to zero capacity.
func allocHintMul(value, factor int) int {
	if value <= 0 || factor <= 0 {
		return 0
	}
	if value > math.MaxInt/factor {
		return 0
	}
	return value * factor
}
