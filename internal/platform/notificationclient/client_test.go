package notificationclient

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestCleanupUsesInternalToken(t *testing.T) {
	const token = "internal-test-token"
	var gotAuth string
	client := New("http://notification-service", token)
	client.http.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotAuth = r.Header.Get("Authorization")
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}, nil
	})
	if err := client.Cleanup(context.Background(), "apiary", uuid.New()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+token {
		t.Fatalf("Authorization = %q, want internal bearer token", gotAuth)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
