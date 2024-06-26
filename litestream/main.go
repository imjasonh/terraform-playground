package main

import (
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"cloud.google.com/go/compute/metadata"
	"github.com/chainguard-dev/clog"
	_ "github.com/chainguard-dev/clog/gcp/init"
	_ "github.com/glebarez/go-sqlite"
)

var dbfile = flag.String("file", "db.sqlite", "path to database file")

func main() {
	flag.Parse()

	dbfile := *dbfile

	if metadata.OnGCE() {
		if os.Getenv("BUCKET") == "" {
			clog.Fatal("BUCKET environment variable is required on GCE")
		}
		dbfile = "/data/" + dbfile
		// Before serving requests, restore the database from the latest replica.
		start := time.Now()
		cmd := exec.Command("litestream", "restore",
			"-o", dbfile,
			fmt.Sprintf("gcs://%s/litestream", os.Getenv("BUCKET")))
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			clog.Fatalf("failed to restore database: %v", err)
		}
		clog.Infof("restoring database took %s", time.Since(start))
	} else {
		if _, err := os.Stat(dbfile); os.IsNotExist(err) {
			clog.Infof("creating database file: %s", dbfile)
			if _, err := os.Create(dbfile); err != nil {
				clog.Fatalf("failed to create database file: %v", err)
			}
		}
	}

	fi, err := os.Stat(dbfile)
	if err != nil {
		clog.Fatalf("failed to stat database: %v", err)
	}
	clog.Infof("database size: %d bytes", fi.Size())

	db, err := sql.Open("sqlite", dbfile)
	if err != nil {
		clog.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec("create table if not exists test3 (time integer primary key)"); err != nil {
		clog.Fatalf("failed to create table: %v", err)
	}

	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no favicon", http.StatusNotFound) })
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := getData(db, true)
		if err != nil {
			clog.Fatalf("failed to get data: %v", err)
		}
		if err := page.Execute(w, data); err != nil {
			clog.Fatalf("failed to execute template: %v", err)
		}
	})
	http.HandleFunc("/click", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := getData(db, false)
		if err != nil {
			clog.Fatalf("failed to get data: %v", err)
		}
		if err := div.Execute(w, data); err != nil {
			clog.Fatalf("failed to execute template: %v", err)
		}
	})
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func getData(db *sql.DB, getVersion bool) (data, error) {
	var version string
	if getVersion {
		row := db.QueryRow("select sqlite_version()")
		if err := row.Scan(&version); err != nil {
			return data{}, fmt.Errorf("failed to query database version: %w", err)
		}
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
  <style>
body {
	font-family: Arial, sans-serif;
}

@keyframes highlight {
    0%   { background: yellow; }
    100% { background: none;   }
}

.highlight {
    animation: highlight 1s;
}

div#data {
	width: 200px;
}
  </style>
</head>
<body>
  <h1>✨ litestream ✨</h1>
  <p>sqlite version: {{.Version}}</p>
  <div id="data" hx-swap="outerHTML">
	<p>count: {{.Count}}</p>
	<button hx-post="/click" hx-trigger="click" hx-target="#data">Click to increment</button>
  </div>
</body>
</html>
`))

	div = template.Must(template.New("").Parse(`
<div id="data" hx-swap="outerHTML">
  <p class="highlight">count: {{.Count}}</p>
  <button hx-post="/click" hx-trigger="click" hx-target="#data">Click to increment</button>
</div>
`))
)
