package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"

	"github.com/chainguard-dev/clog"
	_ "github.com/chainguard-dev/clog/gcp/init"
	_ "github.com/glebarez/go-sqlite"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Before serving requests, restore the database from the latest replica.
	cmd := exec.CommandContext(ctx,
		"litestream", "restore",
		"-o", "/data/db.sqlite",
		fmt.Sprintf("gcs://%s/litestream", os.Getenv("BUCKET")))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		clog.Fatalf("failed to restore database: %v", err)
	}

	db, err := sql.Open("sqlite", "/data/db.sqlite")
	if err != nil {
		clog.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("create table if not exists test3 (time integer primary key)"); err != nil {
		clog.Fatalf("failed to create table: %v", err)
	}

	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no favicon", http.StatusNotFound)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		row := db.QueryRow("select sqlite_version()")
		var version string
		if err := row.Scan(&version); err != nil {
			clog.Fatalf("failed to query database version: %v", err)
		}
		clog.Infof("sqlite version: %s", version)
		fmt.Fprintf(w, "sqlite version: %s\n", version)

		if _, err := db.Exec("insert into test3 (time) values (unixepoch('now','subsec'))"); err != nil {
			clog.Fatalf("failed to insert row: %v", err)
		}
		rows, err := db.Query("select count(*) from test3")
		if err != nil {
			clog.Fatalf("failed to query rows: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var count int64
			if err := rows.Scan(&count); err != nil {
				clog.Fatalf("failed to scan row: %v", err)
			}
			clog.Infof("count: %d", count)
			fmt.Fprintf(w, "count: %d\n", count)
		}
	})
	http.ListenAndServe(":8080", nil)
}
