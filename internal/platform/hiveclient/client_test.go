package hiveclient_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/platform/hiveclient"
)

func TestClient_DeleteByApiary_Success(t *testing.T) {
	apiaryID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method = %q, want DELETE", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/hives" {
			t.Errorf("path = %q, want /api/v1/hives", r.URL.Path)
		}
		if got := r.URL.Query().Get("apiary_id"); got != apiaryID.String() {
			t.Errorf("apiary_id = %q, want %s", got, apiaryID)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	client := hiveclient.New(srv.URL)
	if err := client.DeleteByApiary(context.Background(), "good-token", apiaryID); err != nil {
		t.Fatalf("DeleteByApiary: %v", err)
	}
}

func TestClient_DeleteByApiary_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := hiveclient.New(srv.URL)
	if err := client.DeleteByApiary(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByApiary against a 500: got nil error, want a failure")
	}
}

func TestClient_DeleteByApiary_UnreachableServer(t *testing.T) {
	client := hiveclient.New("http://127.0.0.1:1") // nothing listens here
	if err := client.DeleteByApiary(context.Background(), "some-token", uuid.New()); err == nil {
		t.Fatal("DeleteByApiary against an unreachable server: got nil error, want a failure")
	}
}
