// Command adfilter-server serves the shared verdict database: clients fetch a
// bucket by hash prefix and never send a full hash, so the server cannot tell
// which segment was looked up.
package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

const (
	envDB      = "ADFILTER_SERVER_DB"
	envTrusted = "ADFILTER_TRUSTED_PROXIES"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	dbPath := flag.String("db", os.Getenv(envDB), "path to the sqlite database ($"+envDB+")")
	trusted := flag.String("trusted-proxies", os.Getenv(envTrusted),
		"comma-separated addresses or CIDRs whose CF-Connecting-IP header is believed ($"+envTrusted+")")
	flag.Parse()

	if err := serve(*addr, *dbPath, *trusted); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(addr, dbPath, trustedProxies string) error {
	if dbPath == "" {
		dbPath = "adfilter-server.db"
	}
	st, err := openStore(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	g, err := newGuard(trustedProxies)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(st, g),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "adfilter-server listening on %s, db %s\n", addr, dbPath)
	return srv.ListenAndServe()
}
