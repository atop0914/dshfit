// Command dshfit reports whether DSH plugins can load on a given harness
// version, before the upgrade rather than after.
package main

import (
	"os"

	"github.com/atop0914/dshfit/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
