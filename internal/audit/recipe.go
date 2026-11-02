package audit

// MakeRecipe returns the recipe of target in Makefile text.
func MakeRecipe(text, target string) string { return "" }

// Uncached reports whether a shell command runs Go tests with -count=1 or -shuffle.
func Uncached(cmd string) bool { return false }
