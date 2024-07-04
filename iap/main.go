package main

import (
	"fmt"
	"net/http"
	"time"

	"google.golang.org/api/idtoken"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tok := r.Header.Get("X-Goog-IAP-JWT-Assertion")
		if tok == "" {
			http.Error(w, "No token found", http.StatusUnauthorized)
			return
		}

		aud := "/projects/149343153723/global/backendServices/4161061106542467993" // TODO: don't hardcode this
		payload, err := idtoken.Validate(ctx, tok, aud)
		if err != nil {
			http.Error(w, "Validate: "+err.Error(), http.StatusForbidden)
			return
		}
		if payload.IssuedAt > time.Now().Unix() {
			http.Error(w, "Token issued in the future", http.StatusForbidden)
			return
		}
		if payload.Issuer != "https://cloud.google.com/iap" {
			http.Error(w, "Invalid issuer", http.StatusForbidden)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintln(w, "<html><body>")
		fmt.Fprintln(w, "<h1>You are authenticated!</h1>")
		fmt.Fprintln(w, "<p>Your email is:", payload.Claims["email"], "</p>")
		fmt.Fprintln(w, `<p>Log out: <a href="/?gcp-iap-mode=CLEAR_LOGIN_COOKIE">here</a></p>`)
		fmt.Fprintln(w, "<h3>Failure cases</h3><ul>")
		fmt.Fprintln(w, `<li><a href="/?gcp-iap-mode=SECURE_TOKEN_TEST&iap-secure-token-test-type=FUTURE_ISSUE">Issue date is set in the future.</a></li>`)
		fmt.Fprintln(w, `<li><a href="/?gcp-iap-mode=SECURE_TOKEN_TEST&iap-secure-token-test-type=PAST_EXPIRATION">Expiration date is set in the past.</a></li>`)
		fmt.Fprintln(w, `<li><a href="/?gcp-iap-mode=SECURE_TOKEN_TEST&iap-secure-token-test-type=ISSUER">Incorrect issuer.</a></li>`)
		fmt.Fprintln(w, `<li><a href="/?gcp-iap-mode=SECURE_TOKEN_TEST&iap-secure-token-test-type=AUDIENCE">Incorrect audience.</a></li>`)
		fmt.Fprintln(w, `<li><a href="/?gcp-iap-mode=SECURE_TOKEN_TEST&iap-secure-token-test-type=SIGNATURE">Signed using an incorrect signer.</a></li>`)
		fmt.Fprintln(w, "</body></html>")
	})
	http.ListenAndServe(":8080", nil)
}
