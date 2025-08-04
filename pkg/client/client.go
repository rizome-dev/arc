// Package client provides the core HTTP client for arc
package client

import (
    "context"
    "net/http"
    "time"
)

// Client represents the HTTP client
type Client struct {
    httpClient *http.Client
    baseURL    string
}

// Option defines a function for configuring the client
type Option func(*Client)

// WithHTTPClient sets a custom HTTP client
func WithHTTPClient(client *http.Client) Option {
    return func(c *Client) {
        c.httpClient = client
    }
}

// WithBaseURL sets the base URL for API requests
func WithBaseURL(url string) Option {
    return func(c *Client) {
        c.baseURL = url
    }
}

// New creates a new client instance
func New(opts ...Option) *Client {
    c := &Client{
        httpClient: &http.Client{Timeout: 30 * time.Second},
        baseURL:    "https://api.example.com",
    }

    for _, opt := range opts {
        opt(c)
    }

    return c
}

// Get performs a GET request
func (c *Client) Get(ctx context.Context, path string) (*http.Response, error) {
    req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
    if err != nil {
        return nil, err
    }
    return c.httpClient.Do(req)
}
