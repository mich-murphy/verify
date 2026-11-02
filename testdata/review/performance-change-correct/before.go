package fixture

// Unique returns xs without duplicates, keeping the first occurrence of each.
func Unique(xs []string) []string {
	var out []string
	for _, x := range xs {
		found := false
		for _, y := range out {
			if x == y {
				found = true
				break
			}
		}
		if !found {
			out = append(out, x)
		}
	}
	return out
}
