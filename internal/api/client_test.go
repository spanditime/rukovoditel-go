package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testConfig(endpoint string) Config {
	return Config{
		Endpoint: endpoint,
		Key:      "secret-key",
		Username: "secret-user",
		Password: "secret-password",
	}
}

func TestNewClientEndpoint(t *testing.T) {
	for _, tc := range []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{"http", "http://example.com/api/rest.php", false},
		{"https", "https://example.com/api/rest.php", false},
		{"empty", "", true},
		{"missing scheme", "example.com/api/rest.php", true},
		{"unsupported scheme", "ftp://example.com/api/rest.php", true},
		{"missing host", "https:///api/rest.php", true},
		{"userinfo", "https://secret@example.com/api/rest.php", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(testConfig(tc.endpoint))
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewClient() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if client.endpoint != tc.endpoint {
					t.Fatalf("endpoint = %q, want %q", client.endpoint, tc.endpoint)
				}
				if client.httpClient.Timeout != 10*time.Second {
					t.Fatalf("default timeout = %v", client.httpClient.Timeout)
				}
			}
		})
	}
}

func TestNewClientRequiredCredentials(t *testing.T) {
	for _, tc := range []struct {
		name  string
		clear func(*Config)
	}{
		{"key", func(c *Config) { c.Key = "" }},
		{"username", func(c *Config) { c.Username = "" }},
		{"password", func(c *Config) { c.Password = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig("https://example.com/api/rest.php")
			tc.clear(&cfg)
			if _, err := NewClient(cfg); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewClientUsesProvidedHTTPClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":[]}`)
	}))
	defer server.Close()

	called := false
	httpClient := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return http.DefaultTransport.RoundTrip(req)
	})}
	cfg := testConfig(server.URL)
	cfg.HTTPClient = httpClient
	client, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if client.httpClient != httpClient {
		t.Fatal("provided HTTP client was replaced")
	}
	if _, err := client.Do(context.Background(), Request{Action: "select"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("provided HTTP transport was not used")
	}
}

func TestNewClientErrorDoesNotExposeEndpointSecret(t *testing.T) {
	secret := "secret-in-endpoint"
	_, err := NewClient(testConfig("https://" + secret + "@example.com/api/rest.php"))
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed endpoint secret: %v", err)
	}
}
