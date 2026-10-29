// Command verify is the Verify CLI entry point.
package main

import (
	"io"
	"os"

	"github.com/mich-murphy/verify/internal/cli"
)

var exit = os.Exit

func main() { exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, in io.Reader, out, errw io.Writer) int {
	return cli.Run(args, in, out, errw)
}
