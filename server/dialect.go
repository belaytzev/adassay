package server

import (
	"database/sql"
	"strconv"
	"strings"
)

type dialect struct {
	name     string
	blob     string
	clampSum string
}

var sqliteDialect = dialect{
	name:     "sqlite",
	blob:     "BLOB",
	clampSum: "MAX(0, MIN(i.upheld, ?) + 1 - ? * i.refuted)",
}

var postgresDialect = dialect{
	name:     "postgres",
	blob:     "BYTEA",
	clampSum: "GREATEST(0, LEAST(i.upheld, ?) + 1 - ? * i.refuted)",
}

func (d dialect) rebind(query string) string {
	if d.name != "postgres" {
		return query
	}
	var b strings.Builder
	n := 0
	inString := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if c == '\'' {
			inString = !inString
		}
		if c == '?' && !inString {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func (d dialect) schema() string {
	s := schema + quarantineSchema + installSchema
	if d.name == "postgres" {
		s = strings.ReplaceAll(s, "BLOB", d.blob)
	}
	return s
}

func (d dialect) hasColumn(table, column string) string {
	if d.name == "postgres" {
		return `SELECT COUNT(*) FROM information_schema.columns
		        WHERE table_name = '` + table + `' AND column_name = '` + column + `'`
	}
	return `SELECT COUNT(*) FROM pragma_table_info('` + table + `') WHERE name = '` + column + `'`
}

type binder struct {
	inner interface {
		Exec(string, ...any) (sql.Result, error)
		QueryRow(string, ...any) *sql.Row
		Query(string, ...any) (*sql.Rows, error)
	}
	d dialect
}

func (b binder) Exec(q string, args ...any) (sql.Result, error) {
	return b.inner.Exec(b.d.rebind(q), args...)
}

func (b binder) QueryRow(q string, args ...any) *sql.Row {
	return b.inner.QueryRow(b.d.rebind(q), args...)
}

func (b binder) Query(q string, args ...any) (*sql.Rows, error) {
	return b.inner.Query(b.d.rebind(q), args...)
}
