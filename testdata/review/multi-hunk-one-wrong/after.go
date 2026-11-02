package fixture

// Mean returns the integer average of xs; an empty slice averages to 0.
func Mean(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	sum := 0
	for _, x := range xs {
		sum += x
	}
	return sum / len(xs)
}

// Clamp limits v to the range lo..hi.
func Clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// Percent returns part as a whole-number percentage of total.
func Percent(part, total int) int {
	if total == 0 {
		return 0
	}
	return part / total * 100
}
