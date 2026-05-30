package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strconv"
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

func writeEnabled() bool {
	v := os.Getenv("LITESTREAM_WRITE_ENABLED")
	if v == "" {
		return false
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		clog.Fatalf("invalid LITESTREAM_WRITE_ENABLED %q: %v", v, err)
	}
	return enabled
}

func main() {
	flag.Parse()

	dbfile := *dbfile
	dsn := dbfile
	onGCE := metadata.OnGCE()
	writer := writeEnabled()
	serveReads := !onGCE || !writer
	serveWrites := !onGCE || writer

	log := clog.FromContext(context.Background())
	clog.Infof("litestream routes", "on_gce", onGCE, "writer", writer, "serve_reads", serveReads, "serve_writes", serveWrites)

	if onGCE {
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

		vfs := litestream.NewVFS(client, log.Base())
		vfs.PollInterval = 1 * time.Second
		vfs.WriteEnabled = writer
		if writer {
			vfs.WriteSyncInterval = 1 * time.Second
			if bufPath := os.Getenv("LITESTREAM_BUFFER_PATH"); bufPath != "" {
				vfs.WriteBufferPath = bufPath
			}
		}

		clog.Infof("registering litestream VFS...", "write_enabled", writer)
		if err := sqlite3vfs.RegisterVFS("litestream", vfs); err != nil {
			clog.Fatalf("failed to register VFS: %v", err)
		}
		clog.Infof("litestream VFS registered")

		dsn = "file:db?vfs=litestream&_busy_timeout=5000"
	}

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		clog.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	if serveWrites {
		if onGCE || !dbExists(dbfile) {
			if _, err := db.Exec("create table if not exists test3 (time integer primary key)"); err != nil {
				clog.Fatalf("failed to create table: %v", err)
			}
		}
	}

	http.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no favicon", http.StatusNotFound) })

	if serveReads {
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			data, err := readPageData(db)
			if err != nil {
				if onGCE {
					clog.Errorf("failed to read page data: %v", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				clog.Fatalf("failed to read page data: %v", err)
			}
			if err := page.Execute(w, data); err != nil {
				if onGCE {
					clog.Errorf("failed to execute template: %v", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				clog.Fatalf("failed to execute template: %v", err)
			}
		})
	}

	if serveWrites {
		http.HandleFunc("/click", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			data, err := incrementAndCount(db)
			if err != nil {
				if onGCE {
					clog.Errorf("failed to increment: %v", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				clog.Fatalf("failed to increment: %v", err)
			}
			if err := div.Execute(w, data); err != nil {
				if onGCE {
					clog.Errorf("failed to execute template: %v", err)
					http.Error(w, "internal server error", http.StatusInternalServerError)
					return
				}
				clog.Fatalf("failed to execute template: %v", err)
			}
		})
	}

	clog.Fatal("ListenAndServe", "err", http.ListenAndServe(":8080", nil))
}

func readPageData(db *sql.DB) (data, error) {
	var version string
	row := db.QueryRow("select sqlite_version()")
	if err := row.Scan(&version); err != nil {
		return data{}, fmt.Errorf("failed to query database version: %w", err)
	}

	count, err := queryCount(db)
	if err != nil {
		return data{}, err
	}
	return data{Version: version, Count: count}, nil
}

func incrementAndCount(db *sql.DB) (data, error) {
	if _, err := db.Exec("insert into test3 (time) values (cast(unixepoch('now','subsec') * 1000 as integer))"); err != nil {
		return data{}, fmt.Errorf("failed to insert row: %w", err)
	}
	count, err := queryCount(db)
	if err != nil {
		return data{}, err
	}
	return data{Count: count}, nil
}

func dbExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func queryCount(db *sql.DB) (int, error) {
	row := db.QueryRow("select count(*) from test3")
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to query count: %w", err)
	}
	return count, nil
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
