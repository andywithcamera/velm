package main

import (
	"strconv"
	"strings"
)

func parsePositiveInt(raw string, defaultValue, maxValue int) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || v < 1 {
		return defaultValue
	}
	if maxValue > 0 && v > maxValue {
		return maxValue
	}
	return v
}
