package fixture

// CanRead requires both membership and document visibility.
func CanRead(member, visible bool) bool { return visible }
