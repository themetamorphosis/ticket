package payments

import "testing"

func TestAmounts(t *testing.T) {
	for minor, s := range map[int64]string{0: "0.00", 5: "0.05", 310000: "3100.00", 123456: "1234.56"} {
		if got := FormatAmount(minor); got != s {
			t.Errorf("FormatAmount(%d) = %s", minor, got)
		}
	}
	for s, minor := range map[string]int64{"20": 2000, "20.5": 2050, "20.00": 2000, "2.90": 290} {
		if got, err := ParseAmount(s); err != nil || got != minor {
			t.Errorf("ParseAmount(%s) = %d, %v", s, got, err)
		}
	}
	for _, s := range []string{"x", "1.234", "1.x", ""} {
		if _, err := ParseAmount(s); err == nil {
			t.Errorf("ParseAmount(%q) accepted", s)
		}
	}
}
