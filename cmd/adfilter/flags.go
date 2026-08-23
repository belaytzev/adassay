package main

import (
	"flag"
	"slices"
)

// parseFlags accepts flags after positional arguments too. Go's flag package
// stops at the first non-flag, so `adfilter vote <text> --ad` — the documented
// form — would otherwise swallow every flag as an argument. Everything after a
// literal "--" stays positional.
func parseFlags(fs *flag.FlagSet, args []string) ([]string, error) {
	var tail []string
	if i := slices.Index(args, "--"); i >= 0 {
		args, tail = args[:i], args[i+1:]
	}
	var rest []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return append(rest, tail...), nil
		}
		rest = append(rest, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
