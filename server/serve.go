package server

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"
)

func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil {
		return dsn
	}
	u.User = url.User(u.User.Username())
	return u.String()
}

const (
	EnvDB      = "ADASSAY_SERVER_DB"
	EnvTrusted = "ADASSAY_TRUSTED_PROXIES"
	EnvMetrics = "ADASSAY_METRICS_ADDR"
)

func metricsMux(st *Store, m *metrics) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", m.handler(st))
	return mux
}

func Serve(addr, metricsAddr, dbPath, trustedProxies string) error {
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
	if metricsAddr != "" {
		ms := &http.Server{
			Addr:              metricsAddr,
			Handler:           metricsMux(st, m),
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       15 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
		}
		go func() {
			fmt.Fprintf(os.Stderr, "adassay-server metrics on %s\n", metricsAddr)
			if err := ms.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "metrics listener: %v\n", err)
			}
		}()
	}

	fmt.Fprintf(os.Stderr, "adassay-server listening on %s, db %s\n", addr, redactDSN(dbPath))
	return srv.ListenAndServe()
}
