// Package hiveclient implements application/apiary.HiveCascadeDeleter
// against the real hive-service over HTTP.
package hiveclient

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
)

const requestTimeout = 5 * time.Second

// Client cascades a hive delete for every hive under an apiary by calling
// hive-service's DELETE /api/v1/hives?apiary_id=..., forwarding the
// caller's own access token so hive-service (and, transitively,
// inspection-service and media-service) scope the delete to the same user
// this service already verified owns the apiary.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a Client that calls hive-service at baseURL (e.g.
// "http://hive-service:8080").
func New(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// DeleteByApiary implements application/apiary.HiveCascadeDeleter.
func (c *Client) DeleteByApiary(ctx context.Context, accessToken string, apiaryID uuid.UUID) error {
	u := fmt.Sprintf("%s/api/v1/hives?apiary_id=%s", c.baseURL, url.QueryEscape(apiaryID.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("hiveclient: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("hiveclient: call hive-service: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	default:
		return fmt.Errorf("hiveclient: unexpected status %d from hive-service", resp.StatusCode)
	}
}
