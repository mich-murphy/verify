package fixture

// Mean returns the integer average of xs, or 0 for an empty slice.
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
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Percent returns part as a whole-number percentage of total.
func Percent(part, total int) int {
	if total == 0 {
		return 0
	}
	return part * 100 / total
}
