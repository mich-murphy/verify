package fixture

// ValidName rejects an empty name.
func ValidName(name string) bool { return name != "" }

// Label adds a display prefix.
func Label(name string) string { return "item: " + name }
