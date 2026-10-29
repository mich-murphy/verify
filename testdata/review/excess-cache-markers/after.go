package fixture

// MarkRecent marks at most two of the most recent entries.
func MarkRecent(entries []string) []string {
	out := append([]string(nil), entries...)
	for i := len(out) - 1; i >= 0; i-- {
		out[i] += " [cache]"
	}
	return out
}
