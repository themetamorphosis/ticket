package payments

import (
	"fmt"
	"strconv"
	"strings"
)

// FormatAmount renders minor units as a decimal string, e.g. 155000 -> "1550.00".
func FormatAmount(minor int64) string { return fmt.Sprintf("%d.%02d", minor/100, minor%100) }

// ParseAmount reads a provider amount such as "20", "20.5" or "20.00" into
// minor units, rejecting anything with more than two decimals.
func ParseAmount(s string) (int64, error) {
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 {
		return 0, fmt.Errorf("amount %q has more than two decimals", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", s, err)
	}
	f, err := strconv.ParseInt((frac + "00")[:2], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount %q: %w", s, err)
	}
	return w*100 + f, nil
}
