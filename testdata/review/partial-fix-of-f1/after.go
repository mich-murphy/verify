package fixture

// Percent limits v to a valid percentage.
func Percent(v int) int {
	if v < 0 {
		return 0
	}
	return v
}
