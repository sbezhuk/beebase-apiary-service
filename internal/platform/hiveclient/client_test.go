package hiveclient_test

import (
	"context"
	"encoding/json"
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
		if got := r.URL.Query().Get("apiaryId"); got != apiaryID.String() {
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

func TestClient_ApiaryIDsWithHives_Success(t *testing.T) {
	apiaryID := uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %q, want GET", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer good-token" {
			t.Errorf("Authorization header = %q, want forwarded bearer token", r.Header.Get("Authorization"))
		}
		if r.URL.Path != "/api/v1/hives/apiary-ids-with-hives" {
			t.Errorf("path = %q, want /api/v1/hives/apiary-ids-with-hives", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiaryIds": []string{apiaryID.String()}})
	}))
	defer srv.Close()

	client := hiveclient.New(srv.URL)
	ids, err := client.ApiaryIDsWithHives(context.Background(), "good-token")
	if err != nil {
		t.Fatalf("ApiaryIDsWithHives: %v", err)
	}
	if len(ids) != 1 || ids[0] != apiaryID {
		t.Fatalf("ids = %v, want [%s]", ids, apiaryID)
	}
}

func TestClient_ApiaryIDsWithHives_EmptyYieldsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"apiaryIds": []string{}})
	}))
	defer srv.Close()

	client := hiveclient.New(srv.URL)
	ids, err := client.ApiaryIDsWithHives(context.Background(), "token")
	if err != nil {
		t.Fatalf("ApiaryIDsWithHives: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want empty", ids)
	}
}

func TestClient_ApiaryIDsWithHives_UnexpectedStatusFailsClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := hiveclient.New(srv.URL)
	if _, err := client.ApiaryIDsWithHives(context.Background(), "token"); err == nil {
		t.Fatal("ApiaryIDsWithHives against a 500: got nil error, want a failure")
	}
}

func TestClient_ApiaryIDsWithHives_UnreachableServer(t *testing.T) {
	client := hiveclient.New("http://127.0.0.1:1") // nothing listens here
	if _, err := client.ApiaryIDsWithHives(context.Background(), "token"); err == nil {
		t.Fatal("ApiaryIDsWithHives against an unreachable server: got nil error, want a failure")
	}
}
