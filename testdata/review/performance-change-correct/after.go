package fixture

// Unique returns xs without duplicates, keeping the first occurrence of each.
func Unique(xs []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(xs))
	for _, x := range xs {
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	return out
}
