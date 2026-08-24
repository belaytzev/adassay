package main

import (
	"flag"
	"fmt"
	"os"

	"adassay.com/server"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("db", os.Getenv(server.EnvDB), "path to the sqlite database ($"+server.EnvDB+")")
	trusted := flag.String("trusted-proxies", os.Getenv(server.EnvTrusted),
		"comma-separated addresses or CIDRs whose CF-Connecting-IP header is believed ($"+server.EnvTrusted+")")
	flag.Parse()

	if err := server.Serve(*addr, *dbPath, *trusted); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
