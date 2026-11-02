package cli

import (
	"os"
	"testing"

	"github.com/mich-murphy/verify/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
