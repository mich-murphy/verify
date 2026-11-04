package review

import (
	"strings"
	"testing"
)

func TestParseDescription(t *testing.T) {
	title, descr, err := ParseDescription([]byte(`{"title":"Add the greeting","description":"It greets users. They see it first."}`))
	if err != nil || title != "Add the greeting" || descr != "It greets users. They see it first." {
		t.Fatalf("%q %q %v", title, descr, err)
	}
	long := strings.Repeat("x", 73)
	for name, raw := range map[string]string{
		"not json":      `nope`,
		"empty title":   `{"title":" ","description":"d"}`,
		"empty descr":   `{"title":"t","description":""}`,
		"extra field":   `{"title":"t","description":"d","x":1}`,
		"long title":    `{"title":"` + long + `","description":"d"}`,
		"multiline":     `{"title":"a\nb","description":"d"}`,
		"trailing data": `{"title":"t","description":"d"} {}`,
	} {
		if _, _, err := ParseDescription([]byte(raw)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
