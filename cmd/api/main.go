// Command api is the HTTP service under test (implemented in M1).
package main

import (
	"fmt"
	"os"
)

func main() {
	_, _ = fmt.Fprintln(os.Stderr, "api: not implemented until M1")
	os.Exit(1)
}
