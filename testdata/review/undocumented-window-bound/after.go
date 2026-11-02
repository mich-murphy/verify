package fixture

func Window(items []int, offset, limit int) []int {
	if offset >= len(items) {
		return nil
	}
	return items[offset : offset+limit]
}
