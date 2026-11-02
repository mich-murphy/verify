package fixture

import (
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestLoadReportsReadErrors(t *testing.T) {
	got, err := Load(strings.NewReader(" ok \n"))
	if err != nil || got != "ok" {
		t.Fatalf("Load = %q, %v", got, err)
	}
	boom := errors.New("boom")
	if _, err := Load(io.MultiReader(strings.NewReader("part"), iotest.ErrReader(boom))); !errors.Is(err, boom) {
		t.Fatalf("read error lost: %v", err)
	}
}
