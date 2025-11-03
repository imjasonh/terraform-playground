package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/chainguard-dev/clog"
	"github.com/sethvargo/go-envconfig"
)

var env = envconfig.MustProcess(context.Background(), &struct {
	Port       string `env:"PORT,default=8080"`
	Message    string `env:"MESSAGE,default=Hello from Go container!"`
	InstanceID string `env:"CLOUDFLARE_DURABLE_OBJECT_ID"`
}{})

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		clog.InfoContext(ctx, "handling request", "path", r.URL.Path, "method", r.Method)
		fmt.Fprintf(w, "Hi, I'm a Go container! Message: \"%s\", Instance ID: %s\n", env.Message, env.InstanceID)
	})
	if err := http.ListenAndServe(":"+env.Port, nil); err != nil && err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
	}
}
