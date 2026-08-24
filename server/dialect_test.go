package server

import "testing"

func TestRebindNumbersPlaceholders(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{"none", "SELECT 1", "SELECT 1"},
		{"one", "WHERE hash = ?", "WHERE hash = $1"},
		{"several", "VALUES (?, ?, ?)", "VALUES ($1, $2, $3)"},
		{
			"question mark inside a string literal is data, not a placeholder",
			"WHERE reason = 'why?' AND hash = ?",
			"WHERE reason = 'why?' AND hash = $1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := postgresDialect.rebind(tc.query); got != tc.want {
				t.Errorf("rebind(%q) = %q, want %q", tc.query, got, tc.want)
			}
			if got := sqliteDialect.rebind(tc.query); got != tc.query {
				t.Errorf("sqlite rebind changed %q to %q", tc.query, got)
			}
		})
	}
}

func TestPostgresSchemaHasNoSqliteTypes(t *testing.T) {
	s := postgresDialect.schema()
	if contains(s, "BLOB") {
		t.Error("schema still declares BLOB: Postgres needs BYTEA")
	}
	if !contains(s, "BYTEA") {
		t.Error("schema lost the secret column type")
	}
	if contains(sqliteDialect.schema(), "BYTEA") {
		t.Error("sqlite schema must keep BLOB")
	}
}

func TestQuorumClampIsScalarInBothDialects(t *testing.T) {
	if sqliteDialect.clampSum == postgresDialect.clampSum {
		t.Fatal("the clamp must differ: MAX/MIN are scalar in sqlite and aggregates in Postgres, " +
			"so Postgres needs GREATEST/LEAST or the quorum silently collapses")
	}
	if !contains(postgresDialect.clampSum, "GREATEST") || !contains(postgresDialect.clampSum, "LEAST") {
		t.Error("Postgres clamp must use GREATEST/LEAST")
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestDSNPasswordIsNotLogged(t *testing.T) {
	const dsn = "postgresql://adassay:hunter2@adassay-db-rw.adassay:5432/adassay"
	got := redactDSN(dsn)
	if contains(got, "hunter2") {
		t.Errorf("redactDSN(%q) = %q: the password reached the log", dsn, got)
	}
	if !contains(got, "adassay-db-rw.adassay:5432") {
		t.Errorf("redactDSN dropped the host: %q", got)
	}
	if p := "/data/adassay-server.db"; redactDSN(p) != p {
		t.Errorf("a file path must pass through unchanged, got %q", redactDSN(p))
	}
}
