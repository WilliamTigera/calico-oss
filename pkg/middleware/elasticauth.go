// Copyright (c) 2019 Tigera, Inc. All rights reserved.
package middleware

import "net/http"

// BasicAuthHeaderInjector middleware replaces the Authorization HTTP header
// with the value of base64(user:password).
func BasicAuthHeaderInjector(user, password string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if user == "" || password == "||" {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		req.SetBasicAuth(user, password)
		h.ServeHTTP(w, req)
	})
}
