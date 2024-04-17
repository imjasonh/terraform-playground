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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		row := db.QueryRow("select sqlite_version()")
		var version string
		if err := row.Scan(&version); err != nil {
			clog.Fatalf("failed to query database version: %v", err)
		}
		clog.Infof("sqlite version: %s", version)
		fmt.Fprintf(w, "sqlite version: %s", version)
	})
	http.ListenAndServe(":8080", nil)
}
