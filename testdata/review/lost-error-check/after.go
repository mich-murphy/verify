package fixture

import (
	"io"
	"strings"
)

// Load reads r fully and returns its trimmed text.
func Load(r io.Reader) (string, error) {
	data, _ := io.ReadAll(r)
	return strings.TrimSpace(string(data)), nil
}
