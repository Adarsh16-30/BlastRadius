// Command blast is the chaos runner CLI (implemented in M4).
package main

import (
	"fmt"
	"io"
	"os"
)

func run(w io.Writer, args []string) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(w, "usage: blast run|compare|report|seqwriter (not implemented until M4)") // best-effort stderr
		return 2
	}
	_, _ = fmt.Fprintf(w, "blast %s: not implemented until M4\n", args[0]) // best-effort stderr
	return 1
}

func main() { os.Exit(run(os.Stderr, os.Args[1:])) }
