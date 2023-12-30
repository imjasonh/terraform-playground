package main

import (
	"fmt"
	"net/http"

	"github.com/syumai/workers"
)

func main() {
	http.HandleFunc("/hello", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintln(w, "Hello!")
	})
	workers.Serve(nil)
}
