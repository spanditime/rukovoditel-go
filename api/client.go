// Package api provides a low-level client for documented Rukovoditel REST
// actions. Response fields retain their original wire names.
package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config contains the endpoint, credentials, and optional HTTP client and logger.
type Config struct {
	Endpoint string
	Key      string
	Username string
	Password string

	HTTPClient *http.Client
	Encoding   Encoding
	// Logger receives sanitized operation events; nil disables logging.
	Logger *slog.Logger
}

// Encoding selects the request body format.
type Encoding uint8

const (
	// FormEncoding sends application/x-www-form-urlencoded requests.
	FormEncoding Encoding = iota
	// JSONEncoding sends JSON requests.
	JSONEncoding
)

// Client holds connection configuration for Rukovoditel. It is safe for
// concurrent use when the injected HTTPClient is not modified concurrently.
type Client struct {
	endpoint   string
	key        string
	username   string
	password   string
	httpClient *http.Client
	encoding   Encoding
	logger     *slog.Logger
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
		logger:     cfg.Logger,
	}, nil
}

// Logger returns the configured logger, or nil when logging is disabled.
func (c *Client) Logger() *slog.Logger { return c.logger }
