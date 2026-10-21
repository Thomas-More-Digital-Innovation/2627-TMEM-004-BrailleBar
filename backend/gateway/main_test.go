package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	// fake Document Service that records what reached it
	var reached []string
	docs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer docs.Close()
	docURL, _ := url.Parse(docs.URL)
	gw := httptest.NewServer(routes(docURL))
	defer gw.Close()

	cases := []struct {
		name, method, path, device string
		want                       int
	}{
		{"text is forwarded", "POST", "/documents/text", "dev-1", http.StatusCreated},
		{"no device id", "POST", "/documents/text", "", http.StatusBadRequest},
		{"device id too long", "POST", "/documents/text", strings.Repeat("x", 65), http.StatusBadRequest},
		{"internal content endpoint hidden", "GET", "/documents/11111111-1111-4111-8111-111111111111/content", "dev-1", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, _ := http.NewRequest(c.method, gw.URL+c.path, strings.NewReader(`{"text":"hi"}`))
			if c.device != "" {
				req.Header.Set("X-Device-Id", c.device)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != c.want {
				t.Fatalf("status = %d, want %d", res.StatusCode, c.want)
			}
		})
	}
	if len(reached) != 1 || reached[0] != "POST /documents/text" {
		t.Fatalf("document service got %v, want only the forwarded text", reached)
	}
}
