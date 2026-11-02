package fixture

import "testing"

func TestShippingTiers(t *testing.T) {
	for _, tc := range []struct {
		weight  int
		express bool
		want    int
	}{{-3, false, 5}, {0, false, 5}, {6, false, 8}, {11, false, 12}, {11, true, 24}, {-1, true, 10}} {
		if got := Shipping(tc.weight, tc.express); got != tc.want {
			t.Errorf("Shipping(%d, %t) = %d, want %d", tc.weight, tc.express, got, tc.want)
		}
	}
}
