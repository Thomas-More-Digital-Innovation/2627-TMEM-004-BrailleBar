package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestLimits(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer fake.Close()
	fakeURL, _ := url.Parse(fake.URL)
	gw := httptest.NewServer(routes(fakeURL, fakeURL, fakeURL, 2))
	defer gw.Close()

	post := func(path, device, body string) int {
		req, _ := http.NewRequest("POST", gw.URL+path, strings.NewReader(body))
		req.Header.Set("X-Device-Id", device)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}

	if got := post("/documents/text", "dev-1", strings.Repeat("x", maxBodyBytes+1)); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("huge body: %d, want 413", got)
	}
	// a window can roll over mid-test, which at most allows extra requests, so send enough to be over in any case
	statuses := map[int]int{}
	for device := range 5 {
		statuses[post("/documents/text", "dev-"+strconv.Itoa(device), "{}")]++
	}
	if statuses[http.StatusCreated] < 2 || statuses[http.StatusTooManyRequests] < 1 {
		t.Fatalf("a new device id must not get around the limit: %v", statuses)
	}
	if got := post("/jobs/job-1/status", "dev-1", "{}"); got != http.StatusCreated {
		t.Fatalf("status reports are not rate limited, got %d", got)
	}
}

func TestRoutes(t *testing.T) {
	// fake services that record what reached them
	var reached []string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = append(reached, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer fake.Close()
	fakeURL, _ := url.Parse(fake.URL)
	gw := httptest.NewServer(routes(fakeURL, fakeURL, fakeURL, 1000))
	defer gw.Close()

	cases := []struct {
		name, method, path, device string
		want                       int
	}{
		{"text is forwarded", "POST", "/documents/text", "dev-1", http.StatusCreated},
		{"file is forwarded", "POST", "/documents/file", "dev-1", http.StatusCreated},
		{"no device id", "POST", "/documents/text", "", http.StatusBadRequest},
		{"device id too long", "POST", "/documents/text", strings.Repeat("x", 65), http.StatusBadRequest},
		{"open jobs are forwarded", "GET", "/jobs", "dev-1", http.StatusCreated},
		{"job is forwarded", "GET", "/jobs/job-1", "dev-1", http.StatusCreated},
		{"pages are forwarded", "GET", "/jobs/job-1/pages", "dev-1", http.StatusCreated},
		{"status is forwarded", "POST", "/jobs/job-1/status", "dev-1", http.StatusCreated},
		{"status socket is forwarded", "GET", "/status?deviceId=dev-1", "", http.StatusCreated},
		{"status socket without device id", "GET", "/status", "", http.StatusBadRequest},
		{"status header is overwritten by the query", "GET", "/status", "dev-1", http.StatusBadRequest},
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
	if want := []string{"POST /documents/text", "POST /documents/file", "GET /jobs", "GET /jobs/job-1", "GET /jobs/job-1/pages", "POST /jobs/job-1/status", "GET /status"}; !reflect.DeepEqual(reached, want) {
		t.Fatalf("services got %v, want %v", reached, want)
	}
}
