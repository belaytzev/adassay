package server

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

const (
	EnvDB      = "ADASSAY_SERVER_DB"
	EnvTrusted = "ADASSAY_TRUSTED_PROXIES"
)

func Serve(addr, dbPath, trustedProxies string) error {
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
