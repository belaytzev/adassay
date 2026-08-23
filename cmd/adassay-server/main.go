package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

const (
	envDB      = "ADASSAY_SERVER_DB"
	envTrusted = "ADASSAY_TRUSTED_PROXIES"
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
		dbPath = "adassay-server.db"
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

	m := &metrics{}
	st.mx = m

	srv := &http.Server{
		Addr:              addr,
		Handler:           newMux(st, g, m),
		ReadHeaderTimeout: 5 * time.Second,

		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "adassay-server listening on %s, db %s\n", addr, dbPath)
	return srv.ListenAndServe()
}
