package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config contains the endpoint, credentials, and optional HTTP client.
type Config struct {
	Endpoint string
	Key      string
	Username string
	Password string

	HTTPClient *http.Client
	Encoding   Encoding
}

// Encoding selects the request body format.
type Encoding uint8

const (
	// FormEncoding sends application/x-www-form-urlencoded requests.
	FormEncoding Encoding = iota
	// JSONEncoding sends JSON requests.
	JSONEncoding
)

// Client holds immutable connection configuration for Rukovoditel.
type Client struct {
	endpoint   string
	key        string
	username   string
	password   string
	httpClient *http.Client
	encoding   Encoding
}

// NewClient validates the configuration and constructs a client.
func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("api: endpoint is required")
	}

	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Host == "" || u.User != nil ||
		(u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("api: invalid endpoint")
	}

	if cfg.Key == "" {
		return nil, fmt.Errorf("api: API key is required")
	}

	if cfg.Username == "" {
		return nil, fmt.Errorf("api: username is required")
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("api: password is required")
	}

	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	if cfg.Encoding != FormEncoding &&
		cfg.Encoding != JSONEncoding {
		return nil, fmt.Errorf("api: unsupported encoding")
	}

	return &Client{
		endpoint:   cfg.Endpoint,
		key:        cfg.Key,
		username:   cfg.Username,
		password:   cfg.Password,
		httpClient: cfg.HTTPClient,
		encoding:   cfg.Encoding,
	}, nil
}
