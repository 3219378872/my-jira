// Command schema writes an Ent-generated baseline against an explicitly empty
// development database. It never applies that baseline to the development DB.
package main

import (
	"context"
	"database/sql"
	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"fmt"
	_ "github.com/jackc/pgx/v5/stdlib"
	"my-jira/apps/api/ent"
	"os"
	"strings"
)

func main() {
	url := os.Getenv("SCHEMA_DATABASE_URL")
	if !strings.Contains(url, "myjira_schema") {
		panic("SCHEMA_DATABASE_URL must point to the isolated myjira_schema database")
	}
	db, err := sql.Open("pgx", url)
	must(err)
	defer db.Close()
	client := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	if len(os.Args) != 2 {
		panic("usage: schema output.sql")
	}
	file, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	must(err)
	defer file.Close()
	_, err = fmt.Fprintln(file, "-- Generated from the independently authored Ent schema; do not edit the baseline.")
	must(err)
	must(client.Schema.WriteTo(context.Background(), file))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
