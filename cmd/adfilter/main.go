// Command adfilter strips advertising and hidden injections from a page and
// prints what is left as markdown or JSON.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout)
	switch {
	case err == nil:
	case errors.Is(err, errInjection):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
