package main

import (
	"flag"
	"fmt"
	"os"

	"adassay.com/internal/config"
	"adassay.com/internal/web"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address for the demo site")
	cfgPath := flag.String("config", "", "path to rules.yaml overriding the built-in defaults")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := web.Serve(*addr, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
