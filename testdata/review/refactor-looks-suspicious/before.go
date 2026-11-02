package fixture

// Shipping returns the postage in dollars.
func Shipping(weight int, express bool) int {
	if weight < 0 {
		weight = 0
	}
	cost := 5
	if weight > 10 {
		cost = 12
	} else if weight > 5 {
		cost = 8
	}
	if express {
		cost *= 2
	}
	return cost
}
