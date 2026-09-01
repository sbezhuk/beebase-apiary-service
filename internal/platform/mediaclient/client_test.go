package mediaclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/platform/mediaclient"
)

func TestClient_DeleteByOwner_Success(t *testing.T) {
	apiaryID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/media" {
			t.Errorf("path = %q, want /api/v1/media", r.URL.Path)
		}
		if got := r.URL.Query().Get("owner_type"); got != "APIARY" {
			t.Errorf("owner_type = %q, want APIARY", got)
		}
		if got := r.URL.Query().Get("owner_id"); got != apiaryID.String() {
			t.Errorf("owner_id = %q, want %s", got, apiaryID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.DeleteByOwner(context.Background(), "good-token", apiaryID); err != nil {
		t.Fatalf("DeleteByOwner: %v", err)
	}
}

func TestClient_DeleteByOwner_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := mediaclient.New(srv.URL)
	if err := client.DeleteByOwner(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByOwner against a 500: got nil error, want a failure")
	}
}

func TestClient_DeleteByOwner_UnreachableServer(t *testing.T) {
	client := mediaclient.New("http://127.0.0.1:1") // nothing listens here
	if err := client.DeleteByOwner(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByOwner against an unreachable server: got nil error, want a failure")
	}
}
