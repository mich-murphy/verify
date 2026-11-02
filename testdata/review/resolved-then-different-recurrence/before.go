package fixture

import "strings"

// Slug lowercases s and joins its words with hyphens.
func Slug(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "-")
}
