package fixture

// Redirect returns the destination only when redirection is enabled.
func Redirect(enabled bool, destination string) string {
	if !enabled {
		return ""
	}
	return destination
}
