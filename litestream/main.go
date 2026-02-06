package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/compute/metadata"
	"github.com/benbjohnson/litestream"
	_ "github.com/benbjohnson/litestream/gs" // register "gs" replica client factory
	"github.com/chainguard-dev/clog"
	_ "github.com/chainguard-dev/clog/gcp/init"
	_ "github.com/mattn/go-sqlite3"
	"github.com/psanford/sqlite3vfs"
)

var dbfile = flag.String("file", "db.sqlite", "path to database file")

func main() {
	flag.Parse()

	dbfile := *dbfile
	dsn := dbfile

	if metadata.OnGCE() {
		replicaURL := os.Getenv("LITESTREAM_REPLICA_URL")
		if replicaURL == "" {
			clog.Fatal("LITESTREAM_REPLICA_URL environment variable is required on GCE")
		}

		client, err := litestream.NewReplicaClientFromURL(replicaURL)
		if err != nil {
			clog.Fatalf("failed to create replica client: %v", err)
		}
		if err := client.Init(context.Background()); err != nil {
			clog.Fatalf("failed to init replica client: %v", err)
		}

		logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		vfs := litestream.NewVFS(client, logger)
		vfs.PollInterval = 1 * time.Second
		vfs.WriteEnabled = true
		vfs.WriteSyncInterval = 1 * time.Second

		if bufPath := os.Getenv("LITESTREAM_BUFFER_PATH"); bufPath != "" {
			vfs.WriteBufferPath = bufPath
		}

		clog.Infof("registering litestream VFS...")
		if err := sqlite3vfs.RegisterVFS("litestream", vfs); err != nil {
			clog.Fatalf("failed to register VFS: %v", err)
		}
		clog.Infof("litestream VFS registered")

		dsn = "file:db?vfs=litestream"
	}

	db, err := sql.Open("sqlite3", dsn)
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

	if _, err := db.Exec("insert into test3 (time) values (cast(unixepoch('now','subsec') * 1000 as integer))"); err != nil {
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
