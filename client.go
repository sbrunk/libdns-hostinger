package hostinger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const apiBase = "https://developers.hostinger.com"

// dnsZoneRecord represents a DNS record group as returned/accepted by the Hostinger API.
// Each group contains all records sharing the same (name, type, ttl).
type dnsZoneRecord struct {
	Name    string          `json:"name"`
	Records []recordContent `json:"records"`
	TTL     int             `json:"ttl"`
	Type    string          `json:"type"`
}

// recordContent represents a single record value within a DNS zone record group.
type recordContent struct {
	Content string `json:"content"`
}

// updateRequest is the request body for PUT /api/dns/v1/zones/{domain}.
type updateRequest struct {
	Zone      []dnsZoneRecord `json:"zone"`
	Overwrite bool            `json:"overwrite"`
}

// deleteRequest is the request body for DELETE /api/dns/v1/zones/{domain}.
type deleteRequest struct {
	Filters []deleteFilter `json:"filters"`
}

// deleteFilter specifies which records to delete, matched by name and type.
type deleteFilter struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// apiError represents an error response from the Hostinger API.
type apiError struct {
	Error         string `json:"error"`
	CorrelationID string `json:"correlation_id"`
}

// getToken returns the API token, falling back to the HOSTINGER_API_TOKEN
// environment variable if the struct field is empty.
func (p *Provider) getToken() string {
	if p.APIToken != "" {
		return p.APIToken
	}
	return os.Getenv("HOSTINGER_API_TOKEN")
}

// doRequest performs an authenticated HTTP request to the Hostinger API.
// It includes basic retry logic with exponential backoff for transient errors
// (HTTP 429 and 5xx responses).
func (p *Provider) doRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	p.initClient()

	token := p.getToken()
	if token == "" {
		return nil, fmt.Errorf("API token is required: set APIToken field or HOSTINGER_API_TOKEN environment variable")
	}

	var bodyData []byte
	if body != nil {
		var err error
		bodyData, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshaling request body: %w", err)
		}
	}

	reqURL := apiBase + path

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}

		var reqBody io.Reader
		if bodyData != nil {
			reqBody = bytes.NewReader(bodyData)
		}

		req, err := http.NewRequestWithContext(ctx, method, reqURL, reqBody)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if bodyData != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("executing request: %w", err)
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = fmt.Errorf("reading response body: %w", err)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			var apiErr apiError
			if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Error != "" {
				return nil, fmt.Errorf("API error (HTTP %d, correlation_id=%s): %s",
					resp.StatusCode, apiErr.CorrelationID, apiErr.Error)
			}
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
		}

		return respBody, nil
	}

	return nil, fmt.Errorf("request failed after 3 attempts: %w", lastErr)
}

// initClient ensures the HTTP client is initialized once.
func (p *Provider) initClient() {
	p.once.Do(func() {
		if p.httpClient == nil {
			p.httpClient = &http.Client{
				Timeout: 30 * time.Second,
			}
		}
	})
}
