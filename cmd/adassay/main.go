package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"adassay.com/internal/fetch"
)

func main() {
	err := run(os.Args[1:], os.Stdin, os.Stdout)
	switch {
	case err == nil, errors.Is(err, flag.ErrHelp):
	case errors.Is(err, errInjection):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	case errors.Is(err, fetch.ErrBlocked):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	default:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
