# The shared database server

This directory and `cmd/adassay-server` are licensed **AGPL-3.0** ([LICENSE](LICENSE)), not
MIT like the rest of the repository.

## Why the split

The client should spread without friction, so it is MIT: reimplementing the detector costs
this project nothing, and every install feeds the verdict database.

The server is the opposite case. It is the thing a competitor would fork to run a closed
service on contributed verdicts. AGPL means anyone offering this server over a network has to
publish their modifications, so improvements come back rather than disappearing into a
proprietary product.

The database dump has its own licence again — ODbL, see [../LICENSE-DATA](../LICENSE-DATA).
Three licences for three different kinds of thing: code that should spread, a service that
should stay open, and data that should stay in the commons.

## What this means in practice

- **Running it privately** — no obligation of any kind. AGPL triggers on offering the service
  to others over a network.
- **Running a public instance with local modifications** — publish those modifications.
- **Importing `adassay.com/server` into your own program** — that program becomes subject to
  AGPL. If you only need to talk to a server, use `internal/share` instead, which is MIT.
- **Using the CLI, the MCP server or the client library** — unaffected, they are MIT.

## Boundary

Production code here depends on exactly one package outside this directory:
`adassay.com/internal/core`, which holds the wire types and the shared validation rules. That
is deliberate — the contract belongs to both sides, so it lives on neither.

Tests here do import the MIT client (`internal/share`) to check that what a client queues is
what a server accepts. That direction is fine: AGPL code may use MIT code. The reverse would
not be.
