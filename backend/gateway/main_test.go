package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestRoutes(t *testing.T) {
	// fake services that record what reached them
	var reached []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer fake.Close()
	fakeURL, _ := url.Parse(fake.URL)
	gw := httptest.NewServer(routes(fakeURL, fakeURL))
	defer gw.Close()

	cases := []struct {
		name, method, path, device string
		want                       int
	}{
		{"text is forwarded", "POST", "/documents/text", "dev-1", http.StatusCreated},
		{"no device id", "POST", "/documents/text", "", http.StatusBadRequest},
		{"device id too long", "POST", "/documents/text", strings.Repeat("x", 65), http.StatusBadRequest},
		{"job is forwarded", "GET", "/jobs/job-1", "dev-1", http.StatusCreated},
		{"pages are forwarded", "GET", "/jobs/job-1/pages", "dev-1", http.StatusCreated},
		{"status is forwarded", "POST", "/jobs/job-1/status", "dev-1", http.StatusCreated},
		{"job without device id", "GET", "/jobs/job-1", "", http.StatusBadRequest},
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
	if want := []string{"POST /documents/text", "GET /jobs/job-1", "GET /jobs/job-1/pages", "POST /jobs/job-1/status"}; !reflect.DeepEqual(reached, want) {
		t.Fatalf("services got %v, want %v", reached, want)
	}
}
