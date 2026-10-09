package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDoFormRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" || r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected request headers or method: %s %v", r.Method, r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		got, err := url.ParseQuery(string(body))
		if err != nil {
			t.Error(err)
			return
		}
		want := url.Values{
			"key": {"secret-key"}, "username": {"secret-user"}, "password": {"secret-password"},
			"action": {"select"}, "entity_id": {"21"},
			"items[field_338]":    {"Заявка & тест"},
			"filters[157][value]": {"37"}, "filters[157][condition]": {"include"},
			"update_by_field[id][0]": {"37"}, "update_by_field[id][1]": {"38"},
		}
		if got.Encode() != want.Encode() {
			t.Errorf("body = %q, want %q", got.Encode(), want.Encode())
		}
		io.WriteString(w, `{"status":"success","data":[{"338":"value"}]}`)
	}))
	defer server.Close()
	client, err := NewClient(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(context.Background(), Request{Action: "select", EntityID: 21, Params: map[string]any{
		"items":           map[string]any{"field_338": "Заявка & тест"},
		"filters":         map[string]any{"157": map[string]any{"value": 37, "condition": "include"}},
		"update_by_field": map[string]any{"id": []int{37, 38}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if string(resp.Data) != `[{"338":"value"}]` {
		t.Fatalf("data = %s", resp.Data)
	}
}

func TestDoErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		kind string
	}{
		{"http", 503, "secret-password", "http"},
		{"decode", 200, "{", "decode"},
		{"api", 200, `{"status":"failure","data":"secret-password","extra":1}`, "api"},
		{"limit", 200, strings.Repeat("x", (32<<20)+1), "limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.code); io.WriteString(w, tc.body) }))
			defer server.Close()
			client, err := NewClient(testConfig(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Do(context.Background(), Request{Action: "select"})
			if err == nil || strings.Contains(err.Error(), "secret-password") {
				t.Fatalf("error = %v", err)
			}
			switch tc.kind {
			case "http":
				var e *HTTPError
				if !errors.As(err, &e) || e.StatusCode != 503 || string(e.Body) != tc.body {
					t.Fatalf("HTTP error = %v", err)
				}
			case "api":
				var e *APIError
				if !errors.As(err, &e) || string(e.Body) != tc.body || string(e.Data) != `"secret-password"` {
					t.Fatalf("API error = %v", err)
				}
			case "limit":
				if !strings.Contains(err.Error(), "size limit") {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestDoContextAndTransportError(t *testing.T) {
	cause := errors.New("secret-key transport failed")
	cfg := testConfig("https://example.com/api/rest.php")
	cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })}
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(context.Background(), Request{Action: "select"})
	if !errors.Is(err, cause) || strings.Contains(err.Error(), "secret-key") {
		t.Fatalf("transport error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, req.Context().Err()
	})}
	client, err = NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Do(ctx, Request{Action: "select"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context error = %v", err)
	}
}

func TestDoJSONRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		for _, fragment := range []string{`"action":"select"`, `"key":"secret-key"`, `"entity_id":21`} {
			if !strings.Contains(string(body), fragment) {
				t.Errorf("JSON body missing %s", fragment)
			}
		}
		io.WriteString(w, `{"status":"success","data":[]}`)
	}))
	defer server.Close()
	cfg := testConfig(server.URL)
	cfg.Encoding = JSONEncoding
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(context.Background(), Request{Action: "select", EntityID: 21}); err != nil {
		t.Fatal(err)
	}
}
