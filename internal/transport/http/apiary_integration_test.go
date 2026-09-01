//go:build integration

package http_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	appapiary "github.com/sbezhuk/beebase-apiary-service/internal/application/apiary"
	"github.com/sbezhuk/beebase-apiary-service/internal/platform/hiveclient"
	"github.com/sbezhuk/beebase-apiary-service/internal/platform/mediaclient"
	repopostgres "github.com/sbezhuk/beebase-apiary-service/internal/repository/postgres"
	transporthttp "github.com/sbezhuk/beebase-apiary-service/internal/transport/http"
	apiaryhttp "github.com/sbezhuk/beebase-apiary-service/internal/transport/http/apiary"

	"github.com/sbezhuk/beebase-common/authmw"
	"github.com/sbezhuk/beebase-common/jwks"
	"github.com/sbezhuk/beebase-common/logger"
	"github.com/sbezhuk/beebase-common/pagination"
)

const testKID = "test-kid"

// fakeCascadeTarget stands in for hive-service's or media-service's
// delete endpoint: it always succeeds (204) and records every request it
// received, so tests can assert apiary-service's cascade actually reached
// it, without running a second full service.
type fakeCascadeTarget struct {
	mu       sync.Mutex
	received []*http.Request
}

func newFakeCascadeTarget() *fakeCascadeTarget {
	return &fakeCascadeTarget{}
}

func (f *fakeCascadeTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.received = append(f.received, r.Clone(r.Context()))
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeCascadeTarget) calledWithQuery(key, value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.received {
		if r.URL.Query().Get(key) == value {
			return true
		}
	}
	return false
}

// testStack wires a full router against a real PostgreSQL database (every
// write scoped to a transaction rolled back at the end of the test), a
// real JWKS server, and fake hive-service/media-service - exactly
// mirroring how apiary-service verifies tokens against auth-service and
// cascades a delete in production, just with throwaway stand-ins instead
// of the real downstream services.
type testStack struct {
	server     *httptest.Server
	hives      *fakeCascadeTarget
	hiveServer *httptest.Server
	media      *fakeCascadeTarget
	priv       ed25519.PrivateKey
}

func newTestStack(t *testing.T) *testStack {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping HTTP apiary integration test")
	}

	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	jwksHandler, err := jwks.NewHandler(pub, testKID)
	if err != nil {
		t.Fatalf("jwks.NewHandler: %v", err)
	}
	jwksServer := httptest.NewServer(jwksHandler)
	t.Cleanup(jwksServer.Close)

	verifier, err := authmw.NewVerifierFromJWKSURL(context.Background(), jwksServer.URL)
	if err != nil {
		t.Fatalf("NewVerifierFromJWKSURL: %v", err)
	}

	hives := newFakeCascadeTarget()
	hiveServer := httptest.NewServer(hives)
	t.Cleanup(hiveServer.Close)

	media := newFakeCascadeTarget()
	mediaServer := httptest.NewServer(media)
	t.Cleanup(mediaServer.Close)

	apiaryRepo := repopostgres.NewApiaryRepository(tx)
	hiveDeleter := hiveclient.New(hiveServer.URL)
	mediaDeleter := mediaclient.New(mediaServer.URL)
	apiaryService := appapiary.NewService(apiaryRepo, hiveDeleter, mediaDeleter)
	log := logger.New("development", "error")
	handler := apiaryhttp.NewHandler(apiaryService, log)

	router := transporthttp.NewRouter(log, pool, handler, verifier)

	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	return &testStack{server: srv, hives: hives, hiveServer: hiveServer, media: media, priv: priv}
}

// tokenFor signs a valid access token for userID, exactly as auth-service
// would, but with the test stack's own throwaway key.
func (s *testStack) tokenFor(t *testing.T, userID uuid.UUID) string {
	t.Helper()

	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(time.Now()),
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = testKID

	signed, err := token.SignedString(s.priv)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func (s *testStack) request(t *testing.T, method, path, token string, body any) *http.Response {
	t.Helper()

	var reader *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		reader = bytes.NewReader(buf)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequest(method, s.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeJSON(t *testing.T, resp *http.Response, dst any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()

	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

func TestApiaryFlow_CreateGetListUpdateDelete(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	token := stack.tokenFor(t, userID)

	// Create
	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]string{
		"name":        "Home apiary",
		"location":    "Backyard",
		"description": "two hives",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created apiaryhttp.Response
	decodeJSON(t, resp, &created)
	if created.Name != "Home apiary" {
		t.Fatalf("create: name = %q, want %q", created.Name, "Home apiary")
	}

	// Get
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var fetched apiaryhttp.Response
	decodeJSON(t, resp, &fetched)
	if fetched.ID != created.ID {
		t.Fatalf("get: id = %s, want %s", fetched.ID, created.ID)
	}

	// List
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var list pagination.Response[apiaryhttp.Response]
	decodeJSON(t, resp, &list)
	if len(list.Items) != 1 {
		t.Fatalf("list: got %d apiaries, want 1", len(list.Items))
	}
	if list.Pagination.Total != 1 || list.Pagination.Page != 1 || list.Pagination.Limit != pagination.DefaultLimit {
		t.Fatalf("list: pagination = %+v, want total=1 page=1 limit=%d", list.Pagination, pagination.DefaultLimit)
	}

	// Update
	resp = stack.request(t, http.MethodPut, "/api/v1/apiaries/"+created.ID.String(), token, map[string]string{
		"name":        "Renamed apiary",
		"location":    "Front yard",
		"description": "moved",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var updated apiaryhttp.Response
	decodeJSON(t, resp, &updated)
	if updated.Name != "Renamed apiary" {
		t.Fatalf("update: name = %q, want %q", updated.Name, "Renamed apiary")
	}

	// Delete
	resp = stack.request(t, http.MethodDelete, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	// Get after delete: gone
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestApiaryFlow_CannotAccessAnotherUsersApiary is the end-to-end proof of
// this module's central requirement, exercised over real HTTP with real
// JWT verification for two different users.
func TestApiaryFlow_CannotAccessAnotherUsersApiary(t *testing.T) {
	stack := newTestStack(t)
	owner := uuid.New()
	other := uuid.New()
	ownerToken := stack.tokenFor(t, owner)
	otherToken := stack.tokenFor(t, other)

	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", ownerToken, map[string]string{
		"name": "Owner's apiary",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created apiaryhttp.Response
	decodeJSON(t, resp, &created)

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/v1/apiaries/" + created.ID.String(), nil},
		{http.MethodPut, "/api/v1/apiaries/" + created.ID.String(), map[string]string{"name": "Hijacked"}},
		{http.MethodDelete, "/api/v1/apiaries/" + created.ID.String(), nil},
	}
	for _, tc := range cases {
		resp := stack.request(t, tc.method, tc.path, otherToken, tc.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s as a different user: status = %d, want %d", tc.method, tc.path, resp.StatusCode, http.StatusNotFound)
		}
	}

	// The other user's own (empty) list must not include it either.
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries", otherToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var list pagination.Response[apiaryhttp.Response]
	decodeJSON(t, resp, &list)
	if len(list.Items) != 0 {
		t.Fatalf("other user's list = %v, want empty", list.Items)
	}
	if list.Pagination.Total != 0 {
		t.Fatalf("other user's list total = %d, want 0", list.Pagination.Total)
	}

	// The owner must still be able to see it: the failed attempts above
	// must not have deleted or mutated it.
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+created.ID.String(), ownerToken, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner get after other user's attempts: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var stillOwners apiaryhttp.Response
	decodeJSON(t, resp, &stillOwners)
	if stillOwners.Name != "Owner's apiary" {
		t.Fatalf("name = %q after other user's attempts, want unchanged", stillOwners.Name)
	}
}

func TestApiaryFlow_WithoutTokenIsUnauthorized(t *testing.T) {
	stack := newTestStack(t)

	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("list without token: status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestApiaryFlow_ValidationError(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]string{"name": ""})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with empty name: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestApiaryFlow_InvalidApiaryID(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries/not-a-uuid", token, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("get with malformed id: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestApiaryFlow_ListPagination(t *testing.T) {
	stack := newTestStack(t)
	userID := uuid.New()
	token := stack.tokenFor(t, userID)

	for i := 0; i < 3; i++ {
		resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]string{"name": "A"})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %d: status = %d, want %d", i, resp.StatusCode, http.StatusCreated)
		}
	}

	resp := stack.request(t, http.MethodGet, "/api/v1/apiaries?page=1&limit=2", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var page pagination.Response[apiaryhttp.Response]
	decodeJSON(t, resp, &page)
	if len(page.Items) != 2 {
		t.Fatalf("list page 1: got %d items, want 2", len(page.Items))
	}
	if page.Pagination.Total != 3 || page.Pagination.TotalPages != 2 || !page.Pagination.HasNext || page.Pagination.HasPrevious {
		t.Fatalf("list page 1: pagination = %+v, want total=3 total_pages=2 has_next=true has_previous=false", page.Pagination)
	}
}

func TestApiaryFlow_ListInvalidPageAndLimit(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	cases := []string{
		"/api/v1/apiaries?page=0",
		"/api/v1/apiaries?page=-1",
		"/api/v1/apiaries?page=abc",
		"/api/v1/apiaries?limit=0",
		"/api/v1/apiaries?limit=101",
		"/api/v1/apiaries?limit=abc",
	}
	for _, path := range cases {
		resp := stack.request(t, http.MethodGet, path, token, nil)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s: status = %d, want %d", path, resp.StatusCode, http.StatusBadRequest)
		}
	}
}

// TestApiaryFlow_DeleteCascadesHivesAndMedia is the end-to-end proof that
// deleting an apiary reaches hive-service and media-service before
// hard-deleting the apiary itself, exercised over real HTTP.
func TestApiaryFlow_DeleteCascadesHivesAndMedia(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]string{
		"name": "Gone soon",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created apiaryhttp.Response
	decodeJSON(t, resp, &created)

	resp = stack.request(t, http.MethodDelete, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: status = %d, want %d", resp.StatusCode, http.StatusNoContent)
	}

	if !stack.hives.calledWithQuery("apiary_id", created.ID.String()) {
		t.Errorf("delete did not cascade to hive-service for apiary_id=%s", created.ID)
	}
	if !stack.media.calledWithQuery("owner_id", created.ID.String()) {
		t.Errorf("delete did not cascade to media-service for owner_id=%s", created.ID)
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete: status = %d, want %d", resp.StatusCode, http.StatusNotFound)
	}
}

// TestApiaryFlow_DeleteAbortsWhenHiveServiceUnreachable proves the
// abort-on-failure contract end-to-end: if hive-service can't be reached,
// the apiary must survive the delete attempt.
func TestApiaryFlow_DeleteAbortsWhenHiveServiceUnreachable(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]string{
		"name": "Survives",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created apiaryhttp.Response
	decodeJSON(t, resp, &created)

	// Take down the fake hive-service to simulate it being unreachable.
	stack.hiveServer.Close()

	resp = stack.request(t, http.MethodDelete, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode == http.StatusNoContent {
		t.Fatal("delete succeeded despite hive-service being unreachable")
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get after aborted delete: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
