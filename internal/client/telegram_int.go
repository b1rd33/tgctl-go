package client

import (
	"math"

	"github.com/b1rd33/tgctl-go/internal/safety"
)

func validatePositiveTelegramInt32(value int64, label string) error {
	if value <= 0 || value > math.MaxInt32 {
		return safety.NewBadArgs("%s must be a positive 32-bit integer (got %d)", label, value)
	}
	return nil
}

func validateOptionalTelegramInt32(value int64, label string) error {
	if value < 0 || value > math.MaxInt32 {
		return safety.NewBadArgs("%s must be zero or a positive 32-bit integer (got %d)", label, value)
	}
	return nil
}

func validatePositiveTelegramInts32(values []int64, label string) error {
	for _, value := range values {
		if err := validatePositiveTelegramInt32(value, label); err != nil {
			return err
		}
	}
	return nil
}

func validateNonNegativeNativeTelegramInt32(value int, label string) error {
	if value < 0 || int64(value) > math.MaxInt32 {
		return safety.NewBadArgs("%s must be a non-negative 32-bit integer (got %d)", label, value)
	}
	return nil
}

func defaultedTelegramInt32Limit(value, defaultValue int, label string) (int, error) {
	if value <= 0 {
		return defaultValue, nil
	}
	if int64(value) > math.MaxInt32 {
		return 0, safety.NewBadArgs("%s must be a 32-bit integer (got %d)", label, value)
	}
	return value, nil
}
