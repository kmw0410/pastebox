package internal

import (
	"strconv"
	"strings"
	"time"
)

const maxCustomPolicyDuration = 30 * 24 * time.Hour

type DataPolicy struct {
	Name           string
	CustomDuration time.Duration
}

func ParseDataPolicy(value string) (DataPolicy, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		value = "temporary"
	}

	switch value {
	case "temporary", "permanent", "once":
		return DataPolicy{Name: value}, nil
	}

	if len(value) < 2 {
		return DataPolicy{}, ErrInvalidPolicy
	}

	unit := value[len(value)-1]
	number := value[:len(value)-1]
	if number == "" {
		return DataPolicy{}, ErrInvalidPolicy
	}

	for _, ch := range number {
		if ch < '0' || ch > '9' {
			return DataPolicy{}, ErrInvalidPolicy
		}
	}

	amount, err := strconv.ParseInt(number, 10, 64)
	if err != nil || amount <= 0 {
		return DataPolicy{}, ErrInvalidPolicy
	}

	var duration time.Duration
	switch unit {
	case 'm':
		if amount > int64(maxCustomPolicyDuration/time.Minute) {
			return DataPolicy{}, ErrInvalidPolicy
		}
		duration = time.Duration(amount) * time.Minute
	case 'h':
		if amount > int64(maxCustomPolicyDuration/time.Hour) {
			return DataPolicy{}, ErrInvalidPolicy
		}
		duration = time.Duration(amount) * time.Hour
	case 'd':
		if amount > int64(maxCustomPolicyDuration/(24*time.Hour)) {
			return DataPolicy{}, ErrInvalidPolicy
		}
		duration = time.Duration(amount) * 24 * time.Hour
	default:
		return DataPolicy{}, ErrInvalidPolicy
	}

	if duration <= 0 || duration > maxCustomPolicyDuration {
		return DataPolicy{}, ErrInvalidPolicy
	}

	return DataPolicy{Name: value, CustomDuration: duration}, nil
}

func (p DataPolicy) normalized() DataPolicy {
	if p.Name == "" {
		p.Name = "temporary"
	}
	return p
}

func (p DataPolicy) validated() (DataPolicy, error) {
	parsed, err := ParseDataPolicy(p.Name)
	if err != nil {
		return DataPolicy{}, err
	}
	if p.CustomDuration > 0 && parsed.CustomDuration != p.CustomDuration {
		return DataPolicy{}, ErrInvalidPolicy
	}
	return parsed, nil
}

func (p DataPolicy) ExpiresAt(now time.Time, defaultTTL time.Duration) time.Time {
	p = p.normalized()
	if p.Name == "permanent" {
		return time.Time{}
	}
	if p.CustomDuration > 0 {
		return now.Add(p.CustomDuration)
	}
	return now.Add(defaultTTL)
}
