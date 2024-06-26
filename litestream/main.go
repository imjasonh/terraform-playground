package main

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"time"

	"github.com/chainguard-dev/clog"
	_ "github.com/chainguard-dev/clog/gcp/init"
	_ "github.com/glebarez/go-sqlite"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Before serving requests, restore the database from the latest replica.
	start := time.Now()
	cmd := exec.CommandContext(ctx,
		"litestream", "restore",
		"-o", "/data/db.sqlite",
		fmt.Sprintf("gcs://%s/litestream", os.Getenv("BUCKET")))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		clog.Fatalf("failed to restore database: %v", err)
	}
	clog.Infof("restoring database took %s", time.Since(start))
	fi, err := os.Stat("/data/db.sqlite")
	if err != nil {
		clog.Fatalf("failed to stat database: %v", err)
	}
	clog.Infof("database size: %d bytes", fi.Size())

	db, err := sql.Open("sqlite", "/data/db.sqlite")
	if err != nil {
		clog.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("create table if not exists test3 (time integer primary key)"); err != nil {
		clog.Fatalf("failed to create table: %v", err)
	}

	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no favicon", http.StatusNotFound) })
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		data, err := getData(db)
		if err != nil {
			clog.Fatalf("failed to get data: %v", err)
		}
		if err := page.Execute(w, data); err != nil {
			clog.Fatalf("failed to execute template: %v", err)
		}
	})
	http.HandleFunc("/click", func(w http.ResponseWriter, r *http.Request) {
		data, err := getData(db)
		if err != nil {
			clog.Fatalf("failed to get data: %v", err)
		}
		if err := div.Execute(w, data); err != nil {
			clog.Fatalf("failed to execute template: %v", err)
		}
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func getData(db *sql.DB) (data, error) {
	row := db.QueryRow("select sqlite_version()")
	var version string
	if err := row.Scan(&version); err != nil {
		return data{}, fmt.Errorf("failed to query database version: %w", err)
	}

	if _, err := db.Exec("insert into test3 (time) values (unixepoch('now','subsec'))"); err != nil {
		return data{}, fmt.Errorf("failed to insert row: %w", err)
	}
	rows, err := db.Query("select count(*) from test3")
	if err != nil {
		return data{}, fmt.Errorf("failed to query rows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var count int
		if err := rows.Scan(&count); err != nil {
			return data{}, fmt.Errorf("failed to scan row: %w", err)
		}
		return data{Version: version, Count: count}, nil
	}
	return data{}, fmt.Errorf("failed to get count")
}

type data struct {
	Version string
	Count   int
}

var (
	page = template.Must(template.New("").Parse(`<!DOCTYPE html>
<html>
<head>
  <title>litestream</title>
  <script src="https://unpkg.com/htmx.org@2.0.0" integrity="sha384-wS5l5IKJBvK6sPTKa2WZ1js3d947pvWXbPJ1OmWfEuxLgeHcEbjUUA5i9V5ZkpCw" crossorigin="anonymous"></script>
</head>
<body>
  <div id="data" hx-swap="outerHTML" hx-get="/">
	<h1>litestream</h1>
	<p>sqlite version: {{.Version}}</p>
	<p>count: {{.Count}}</p>
	<button hx-post="/click" hx-trigger="click" hx-target="data">Refresh</button>
  </div>
</body>
</html>
`))

	div = template.Must(template.New("").Parse(`
<div id="data" hx-swap="outerHTML" hx-get="/">
  <h1>litestream</h1>
  <p>sqlite version: {{.Version}}</p>
  <p>count: {{.Count}}</p>
  <button hx-post="/click" hx-trigger="click" hx-target="data">Refresh</button>
</div>
`))
)
