package main

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/chainguard-dev/clog"
	_ "github.com/chainguard-dev/clog/gcp/init"
	_ "github.com/glebarez/go-sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "/data/db.sqlite")
	if err != nil {
		clog.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("create table if not exists test (time text primary key)"); err != nil {
		clog.Fatalf("failed to create table: %v", err)
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		row := db.QueryRow("select sqlite_version()")
		var version string
		if err := row.Scan(&version); err != nil {
			clog.Fatalf("failed to query database version: %v", err)
		}
		clog.Infof("sqlite version: %s", version)
		fmt.Fprintf(w, "sqlite version: %s", version)

		if _, err := db.Exec("insert into test (time) values (datetime('now'))"); err != nil {
			clog.Fatalf("failed to insert row: %v", err)
		}
		rows, err := db.Query("select * from test")
		if err != nil {
			clog.Fatalf("failed to query rows: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var time string
			if err := rows.Scan(&time); err != nil {
				clog.Fatalf("failed to scan row: %v", err)
			}
			clog.Infof("time: %s", time)
			fmt.Fprintf(w, "time: %s\n", time)
		}
	})
	http.ListenAndServe(":8080", nil)
}
