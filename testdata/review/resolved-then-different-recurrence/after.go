package fixture

import "strings"

// Slug lowercases s and joins its words with hyphens.
func Slug(s string) string {
	return strings.Replace(strings.ToLower(s), " ", "-", 1)
}
