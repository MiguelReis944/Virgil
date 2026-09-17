package policies

import (
	"errors"
	"math/big"
	"regexp"
)

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,9})?$`)

func decimal(value string) (*big.Rat, error) {
	if value == "" {
		return new(big.Rat), nil
	}
	if !decimalPattern.MatchString(value) {
		return nil, errors.New("invalid decimal")
	}
	r, ok := new(big.Rat).SetString(value)
	if !ok || r.Sign() < 0 {
		return nil, errors.New("invalid nonnegative decimal")
	}
	return r, nil
}

func exceedsCost(current, add, limit string) (bool, error) {
	a, err := decimal(current)
	if err != nil {
		return false, err
	}
	b, err := decimal(add)
	if err != nil {
		return false, err
	}
	c, err := decimal(limit)
	if err != nil {
		return false, err
	}
	return new(big.Rat).Add(a, b).Cmp(c) > 0, nil
}

func addCost(current, amount string) (string, error) {
	a, err := decimal(current)
	if err != nil {
		return "", err
	}
	b, err := decimal(amount)
	if err != nil {
		return "", err
	}
	return new(big.Rat).Add(a, b).RatString(), nil
}
