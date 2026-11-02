package fixture

// Max returns the larger of a and b.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Remaining returns how many units are left under limit, never negative.
func Remaining(limit, used int) int {
	return Max(limit-used, 0)
}
