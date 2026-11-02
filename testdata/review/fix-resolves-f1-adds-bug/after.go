package fixture

// Average returns the integer mean of xs.
func Average(xs []int) int {
	if len(xs) < 2 {
		return 0
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum / len(xs)
}
