/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package harbor is a minimal client for the Harbor v2.0 REST API covering
// the resources managed by the operator.
package harbor

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

const apiPrefix = "/api/v2.0"

// Config holds the parameters needed to reach a Harbor instance.
type Config struct {
	URL                string
	Username           string
	Password           string
	InsecureSkipVerify bool
	CABundle           []byte
	Timeout            time.Duration
}

// Client talks to the Harbor REST API.
type Client struct {
	base     *url.URL
	username string
	password string
	http     *http.Client
}

// NewClient builds a Client from cfg.
func NewClient(cfg Config) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(cfg.URL, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid Harbor URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("invalid Harbor URL %q: scheme must be http or https", cfg.URL)
	}

	tlsConfig := &tls.Config{InsecureSkipVerify: cfg.InsecureSkipVerify} //nolint:gosec // opt-in by the user
	if len(cfg.CABundle) > 0 {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(cfg.CABundle) {
			return nil, errors.New("CA bundle contains no valid PEM certificates")
		}
		tlsConfig.RootCAs = pool
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		base:     base,
		username: cfg.Username,
		password: cfg.Password,
		http:     &http.Client{Transport: transport, Timeout: timeout},
	}, nil
}

// Host returns the registry host (and port) of the Harbor instance.
func (c *Client) Host() string {
	return c.base.Host
}

// Username returns the user the client authenticates as.
func (c *Client) Username() string {
	return c.username
}

// Error is returned for non-2xx Harbor responses.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("harbor %s %s: %d %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// IsNotFound reports whether err is a Harbor 404.
func IsNotFound(err error) bool {
	return hasStatus(err, http.StatusNotFound)
}

// IsConflict reports whether err is a Harbor 409.
func IsConflict(err error) bool {
	return hasStatus(err, http.StatusConflict)
}

// IsSamePassword reports whether err is Harbor rejecting a password change
// because the new password equals the current one.
func IsSamePassword(err error) bool {
	var herr *Error
	return errors.As(err, &herr) && herr.StatusCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(herr.Message), "same")
}

func hasStatus(err error, code int) bool {
	var herr *Error
	return errors.As(err, &herr) && herr.StatusCode == code
}

type errorBody struct {
	Errors []struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

// do performs a request. in is JSON-encoded when non-nil; out is decoded from
// the response when non-nil. The response headers are returned for callers
// that need the Location header.
func (c *Client) do(ctx context.Context, method, p string, query url.Values, in, out any) (http.Header, error) {
	u := *c.base
	u.Path = path.Join(c.base.Path, apiPrefix, p)
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(string(data))
		var eb errorBody
		if json.Unmarshal(data, &eb) == nil && len(eb.Errors) > 0 {
			msgs := make([]string, 0, len(eb.Errors))
			for _, e := range eb.Errors {
				msgs = append(msgs, e.Message)
			}
			msg = strings.Join(msgs, "; ")
		}
		return resp.Header, &Error{StatusCode: resp.StatusCode, Method: method, Path: p, Message: msg}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.Header, fmt.Errorf("decoding harbor response for %s %s: %w", method, p, err)
		}
	}
	return resp.Header, nil
}

// idFromLocation extracts the trailing numeric ID from a Location header.
func idFromLocation(h http.Header) (int64, error) {
	loc := h.Get("Location")
	if loc == "" {
		return 0, errors.New("harbor response is missing the Location header")
	}
	id, err := strconv.ParseInt(path.Base(loc), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse ID from Location %q: %w", loc, err)
	}
	return id, nil
}

// pageSize is the number of items requested per page from list endpoints.
const pageSize = 100

// pageQuery returns query parameters selecting a page of a list endpoint.
func pageQuery(page int) url.Values {
	return url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(pageSize)}}
}

// exactQuery builds a Harbor "q" query matching key exactly.
func exactQuery(key, value string) url.Values {
	q := pageQuery(1)
	q.Set("q", key+"="+value)
	return q
}
