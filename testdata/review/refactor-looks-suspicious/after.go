package fixture

// Shipping returns the postage in dollars.
func Shipping(weight int, express bool) int {
	cost := 5
	switch {
	case weight > 10:
		cost = 12
	case weight > 5:
		cost = 8
	}
	if express {
		return cost * 2
	}
	return cost
}
