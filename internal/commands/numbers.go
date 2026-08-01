package commands

import (
	"math"
	"strconv"
	"strings"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

func parsePositiveInt32Decimal(raw, label string) (int64, error) {
	value, err := parsePositiveDecimal(raw, label)
	if err != nil || value > math.MaxInt32 {
		return 0, safety.NewBadArgs("%s must be a positive 32-bit decimal integer (got %q)", label, raw)
	}
	return value, nil
}

func parsePositiveDecimal(raw, label string) (int64, error) {
	if raw == "" {
		return 0, safety.NewBadArgs("%s must be a positive decimal integer (got %q)", label, raw)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, safety.NewBadArgs("%s must be a positive decimal integer (got %q)", label, raw)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, safety.NewBadArgs("%s must be a positive decimal integer (got %q)", label, raw)
	}
	return value, nil
}

func parseSignedNonZeroCSV(raw, label string) ([]int64, error) {
	if raw == "" {
		return nil, safety.NewBadArgs("%s cannot be empty", label)
	}
	parts := strings.Split(raw, ",")
	values := make([]int64, 0, len(parts))
	for _, part := range parts {
		value, err := parseSignedInt64(part, label)
		if err != nil {
			return nil, err
		}
		if value == 0 {
			return nil, safety.NewBadArgs("%s must not be zero (got %q)", label, part)
		}
		values = append(values, value)
	}
	return values, nil
}

func parseNonNegativeDecimal(raw, label string) (int64, error) {
	if raw == "" {
		return 0, safety.NewBadArgs("%s must be a non-negative 32-bit decimal integer (got %q)", label, raw)
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, safety.NewBadArgs("%s must be a non-negative 32-bit decimal integer (got %q)", label, raw)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value > math.MaxInt32 {
		return 0, safety.NewBadArgs("%s must be a non-negative 32-bit decimal integer (got %q)", label, raw)
	}
	return value, nil
}

func validateOptionalPositiveInt32(value int64, label string) error {
	if value < 0 || value > math.MaxInt32 {
		return safety.NewBadArgs("%s must be zero or a positive 32-bit integer (got %d)", label, value)
	}
	return nil
}

func validateNonNegativeNativeInt32(value int, label string) error {
	if value < 0 || int64(value) > math.MaxInt32 {
		return safety.NewBadArgs("%s must be a non-negative 32-bit integer (got %d)", label, value)
	}
	return nil
}

func defaultedInt32Limit(value, defaultValue int, label string) (int, error) {
	if value <= 0 {
		return defaultValue, nil
	}
	if int64(value) > math.MaxInt32 {
		return 0, safety.NewBadArgs("%s must be a 32-bit integer (got %d)", label, value)
	}
	return value, nil
}

func parsePositiveInt32CSV(raw, label string) ([]int64, error) {
	if raw == "" {
		return nil, safety.NewBadArgs("%ss cannot be empty", label)
	}
	parts := strings.Split(raw, ",")
	values := make([]int64, 0, len(parts))
	for _, part := range parts {
		value, err := parsePositiveInt32Decimal(part, label)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func parseSignedInt64(raw, label string) (int64, error) {
	if raw == "" {
		return 0, safety.NewBadArgs("%s must be an integer (got %q)", label, raw)
	}
	start := 0
	if raw[0] == '-' {
		start = 1
	}
	if start == len(raw) {
		return 0, safety.NewBadArgs("%s must be an integer (got %q)", label, raw)
	}
	for i := start; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, safety.NewBadArgs("%s must be an integer (got %q)", label, raw)
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, safety.NewBadArgs("%s must be an integer (got %q)", label, raw)
	}
	return value, nil
}
