package fixture

// CanAdmit reports whether n more units fit under limit given used units.
func CanAdmit(limit, used, n int) bool {
	return limit-used >= n
}
