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

// fakeCascadeTarget stands in for hive-service's delete endpoint and, for
// media-service, the GET /api/v1/media?ids= and DELETE /api/v1/media?ids=
// endpoints apiary-service now calls to verify image ownership on
// create/update and to hard-delete an apiary's own files on cascade
// delete: it answers GET from an in-memory set of media ids a test can
// seed as belonging to the caller via own(), and 204 to everything else
// (including DELETE, so it still works as a plain cascade-delete stand-in
// for hive-service). It records every request it received, so tests can
// assert apiary-service's cascade actually reached it, without running a
// second full service.
type fakeCascadeTarget struct {
	mu       sync.Mutex
	received []*http.Request
	ownedIDs map[uuid.UUID]bool // mediaID -> belongs to the caller
}

func newFakeCascadeTarget() *fakeCascadeTarget {
	return &fakeCascadeTarget{ownedIDs: map[uuid.UUID]bool{}}
}

// own registers each of ids as belonging to the caller, so this fake's GET
// /api/v1/media?ids= endpoint returns it - letting a test exercise
// apiary-service's media-ownership verification without a real
// media-service.
func (f *fakeCascadeTarget) own(ids ...uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		f.ownedIDs[id] = true
	}
}

func (f *fakeCascadeTarget) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.received = append(f.received, r.Clone(r.Context()))
	f.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/media":
		f.serveList(w, r)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// serveList answers GET /api/v1/media?ids=&ids=...: returns every
// requested id this fake was told is own()ed by the caller, silently
// omitting unknown/foreign ones - mirroring media-service's real
// behavior closely enough for apiary-service's own ownership
// verification to be exercised against it.
func (f *fakeCascadeTarget) serveList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	items := []map[string]any{}
	for _, raw := range r.URL.Query()["ids"] {
		id, err := uuid.Parse(raw)
		if err != nil {
			continue
		}
		if f.ownedIDs[id] {
			items = append(items, map[string]any{"id": id})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
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

// calledWithQueryValue is calledWithQuery's counterpart for a repeated
// query param (e.g. ?ids=&ids=...), matching if value is any one of the
// repeated values on any received request.
func (f *fakeCascadeTarget) calledWithQueryValue(key, value string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.received {
		for _, v := range r.URL.Query()[key] {
			if v == value {
				return true
			}
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
	handler := apiaryhttp.NewHandler(apiaryService, log, "http://localhost:8080")

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
	photo := uuid.New()
	stack.media.own(photo)

	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]any{
		"name":   "Gone soon",
		"images": []string{photo.String()},
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
	if !stack.media.calledWithQueryValue("ids", photo.String()) {
		t.Errorf("delete did not cascade to media-service for the apiary's own image %s", photo)
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

// TestApiaryFlow_UpdateReplacesImages is the end-to-end proof of the
// images feature: a GET reports whatever apiary-service itself
// persisted, an update that doesn't mention images leaves it alone, and
// an update with an explicit (deduplicated) images list replaces the set
// wholesale - without deleting the dropped id's underlying file - while
// rejecting IDs that don't belong to the caller.
func TestApiaryFlow_UpdateReplacesImages(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	keep := uuid.New()
	drop := uuid.New()
	stack.media.own(keep, drop)

	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]any{
		"name":   "Home apiary",
		"images": []string{keep.String(), drop.String()},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	var created apiaryhttp.Response
	decodeJSON(t, resp, &created)
	if len(created.Images) != 2 {
		t.Fatalf("create: images = %v, want 2 items", created.Images)
	}

	// Get reports both, without having been asked to change anything.
	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries/"+created.ID.String(), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var fetched apiaryhttp.Response
	decodeJSON(t, resp, &fetched)
	if len(fetched.Images) != 2 {
		t.Fatalf("get: images = %v, want 2 items", fetched.Images)
	}

	// Update without an images field leaves both attached.
	resp = stack.request(t, http.MethodPut, "/api/v1/apiaries/"+created.ID.String(), token, map[string]string{"name": "Renamed"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update without images: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var untouched apiaryhttp.Response
	decodeJSON(t, resp, &untouched)
	if len(untouched.Images) != 2 {
		t.Fatalf("update without images: images = %v, want 2 items (untouched)", untouched.Images)
	}

	// Update with an explicit (deduplicated) images list prunes "drop".
	resp = stack.request(t, http.MethodPut, "/api/v1/apiaries/"+created.ID.String(), token, map[string]any{
		"name":   "Renamed again",
		"images": []string{keep.String(), keep.String()},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update with images: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var pruned apiaryhttp.Response
	decodeJSON(t, resp, &pruned)
	if len(pruned.Images) != 1 || pruned.Images[0].ID != keep {
		t.Fatalf("update with images: images = %v, want [%s]", pruned.Images, keep)
	}
	if stack.media.calledWithQueryValue("ids", drop.String()) {
		t.Errorf("dropping %s from images must not delete its underlying file (no DELETE call expected)", drop)
	}

	// An update referencing a media ID that doesn't belong to the caller
	// is rejected as a validation error.
	resp = stack.request(t, http.MethodPut, "/api/v1/apiaries/"+created.ID.String(), token, map[string]any{
		"name":   "Should not apply",
		"images": []string{uuid.New().String()},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("update with foreign image: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

// TestApiaryFlow_CreateWithImages_RejectsForeignMedia proves Create
// validates every referenced image exactly like Update does, and - since
// there's no apiary row yet to roll back - never persists one.
func TestApiaryFlow_CreateWithImages_RejectsForeignMedia(t *testing.T) {
	stack := newTestStack(t)
	token := stack.tokenFor(t, uuid.New())

	resp := stack.request(t, http.MethodPost, "/api/v1/apiaries", token, map[string]any{
		"name":   "Home apiary",
		"images": []string{uuid.New().String()},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("create with foreign image: status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	resp = stack.request(t, http.MethodGet, "/api/v1/apiaries", token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list: status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	var list pagination.Response[apiaryhttp.Response]
	decodeJSON(t, resp, &list)
	if len(list.Items) != 0 {
		t.Fatalf("an apiary was persisted despite a rejected image: %v", list.Items)
	}
}
