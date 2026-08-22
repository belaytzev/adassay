// Command adfilter-mcp exposes the filter to an agent over the Model Context
// Protocol on stdio.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/belaytzev/adfilter/internal/config"
	"github.com/belaytzev/adfilter/internal/judge"
	"github.com/belaytzev/adfilter/internal/store"
)

func main() {
	cfgPath := flag.String("config", "", "path to rules.yaml overriding the built-in defaults")
	dbPath := flag.String("db", "", "path to the local verdict database (default: user cache dir, $"+store.EnvDB+")")
	flag.Parse()

	if err := serve(*cfgPath, *dbPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func serve(cfgPath, dbPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	s := &server{cfg: cfg, db: db, judge: judge.New(cfg.Judge)}
	return newServer(s).Run(context.Background(), &mcp.StdioTransport{})
}
