package fixture

// Average returns the integer mean of xs.
func Average(xs []int) int {
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum / len(xs)
}
