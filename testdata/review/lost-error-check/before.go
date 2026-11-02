package fixture

import (
	"io"
	"strings"
)

// Load reads r fully and returns its trimmed text.
func Load(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
