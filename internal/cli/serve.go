package cli

import (
	"io"

	"github.com/atop0914/dshfit/internal/rpc"
)

// runServe answers panel requests on stdin until EOF.
//
// It takes no flags on purpose. Everything a caller needs to say goes in the
// request; a flag here would be a second way to configure the same thing, and
// the two would drift. Arguments are rejected rather than ignored: silently
// accepting `serve --host 0.2.2` would leave the caller believing the sidecar
// took its host when every request still has to name one.
//
// The exit code is 0 for a clean EOF even when plugins were judged blocked: the
// verdicts belong to individual responses, and a long-lived process has no
// single answer to put in its exit code. Only a broken stdout is a non-zero
// exit, because then nothing can be reported at all.
func runServe(in io.Reader, args []string, out, errOut io.Writer) int {
	if len(args) > 0 {
		warnf(errOut, "serve: takes no arguments (got %q); every setting travels in the request\n\n%s",
			args, usage)
		return 2
	}
	if err := rpc.Serve(in, out, rpc.Config{}); err != nil {
		warnf(errOut, "serve: %v\n", err)
		return 2
	}
	return 0
}
