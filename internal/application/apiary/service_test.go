package apiary_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/google/uuid"

	appapiary "github.com/sbezhuk/beebase-apiary-service/internal/application/apiary"
	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	"github.com/sbezhuk/beebase-common/pagination"
)

// --- in-memory fake for the domain port ---

type fakeRepo struct {
	mu   sync.Mutex
	byID map[uuid.UUID]*apiary.Apiary
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byID: map[uuid.UUID]*apiary.Apiary{}}
}

func (f *fakeRepo) Create(_ context.Context, a *apiary.Apiary) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *a
	f.byID[a.ID] = &cp
	return nil
}

func (f *fakeRepo) GetByID(_ context.Context, userID, apiaryID uuid.UUID) (*apiary.Apiary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[apiaryID]
	if !ok || a.UserID != userID || a.DeletedAt != nil {
		return nil, apiary.ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (f *fakeRepo) ListByUser(_ context.Context, userID uuid.UUID, p pagination.Params) ([]*apiary.Apiary, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var all []*apiary.Apiary
	for _, a := range f.byID {
		if a.UserID == userID && a.DeletedAt == nil {
			cp := *a
			all = append(all, &cp)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.Before(all[j].CreatedAt)
		}
		return all[i].ID.String() < all[j].ID.String()
	})

	total := len(all)
	start := p.Offset()
	if start > total {
		start = total
	}
	end := start + p.Limit
	if end > total {
		end = total
	}

	return all[start:end], total, nil
}

func (f *fakeRepo) Update(_ context.Context, a *apiary.Apiary) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	existing, ok := f.byID[a.ID]
	if !ok || existing.UserID != a.UserID || existing.DeletedAt != nil {
		return apiary.ErrNotFound
	}
	cp := *a
	f.byID[a.ID] = &cp
	return nil
}

func (f *fakeRepo) HardDelete(_ context.Context, userID, apiaryID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[apiaryID]
	if !ok || a.UserID != userID {
		return apiary.ErrNotFound
	}
	delete(f.byID, apiaryID)
	return nil
}

// --- fake hive/media deleters ---

// fakeHiveDeleter simulates hive-service's DeleteByApiary: it records
// every apiaryID it was asked to cascade, and can be configured to fail
// for specific apiaryIDs to exercise the cascade's abort-on-failure
// behavior.
type fakeHiveDeleter struct {
	mu      sync.Mutex
	deleted []uuid.UUID
	failFor map[uuid.UUID]error
}

func newFakeHiveDeleter() *fakeHiveDeleter {
	return &fakeHiveDeleter{failFor: map[uuid.UUID]error{}}
}

func (f *fakeHiveDeleter) failOn(apiaryID uuid.UUID, err error) {
	f.failFor[apiaryID] = err
}

func (f *fakeHiveDeleter) DeleteByApiary(_ context.Context, _ string, apiaryID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failFor[apiaryID]; ok {
		return err
	}
	f.deleted = append(f.deleted, apiaryID)
	return nil
}

func (f *fakeHiveDeleter) wasDeleted(apiaryID uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.deleted {
		if id == apiaryID {
			return true
		}
	}
	return false
}

// fakeMediaDeleter is the media-service equivalent of fakeHiveDeleter. It
// also stands in for the rest of application/apiary.MediaClient: attached
// tracks which media IDs are attached to which apiary (seeded via
// attach(), as if already linked), and unattached tracks the caller's own
// uploads that exist but aren't linked to anything yet (seeded via
// uploadUnattached()) - together enough to exercise Update's images
// reconciliation, including attaching a fresh upload, without a real
// media-service.
type fakeMediaDeleter struct {
	mu         sync.Mutex
	deleted    []uuid.UUID
	failFor    map[uuid.UUID]error
	attached   map[uuid.UUID]uuid.UUID // mediaID -> apiaryID
	unattached map[uuid.UUID]bool      // mediaID -> exists, belongs to the caller, not yet attached
}

func newFakeMediaDeleter() *fakeMediaDeleter {
	return &fakeMediaDeleter{
		failFor:    map[uuid.UUID]error{},
		attached:   map[uuid.UUID]uuid.UUID{},
		unattached: map[uuid.UUID]bool{},
	}
}

func (f *fakeMediaDeleter) failOn(apiaryID uuid.UUID, err error) {
	f.failFor[apiaryID] = err
}

// attach registers mediaID as already attached to apiaryID, as if it had
// been uploaded and linked there.
func (f *fakeMediaDeleter) attach(apiaryID, mediaID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attached[mediaID] = apiaryID
}

// uploadUnattached registers mediaID as an existing upload belonging to
// the caller, not yet attached to anything - the fixture Update's images
// reconciliation needs to prove it can attach a fresh upload, not just
// keep one already linked.
func (f *fakeMediaDeleter) uploadUnattached(mediaID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unattached[mediaID] = true
}

func (f *fakeMediaDeleter) DeleteByOwner(_ context.Context, _ string, apiaryID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.failFor[apiaryID]; ok {
		return err
	}
	f.deleted = append(f.deleted, apiaryID)
	return nil
}

func (f *fakeMediaDeleter) wasDeleted(apiaryID uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range f.deleted {
		if id == apiaryID {
			return true
		}
	}
	return false
}

func (f *fakeMediaDeleter) ListAttached(_ context.Context, _ string, apiaryID uuid.UUID) ([]uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []uuid.UUID
	for mediaID, owner := range f.attached {
		if owner == apiaryID {
			ids = append(ids, mediaID)
		}
	}
	return ids, nil
}

func (f *fakeMediaDeleter) Attach(_ context.Context, _ string, apiaryID, mediaID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if owner, ok := f.attached[mediaID]; ok {
		if owner == apiaryID {
			return nil // idempotent replay
		}
		return appapiary.ErrImageNotFound // attached to a different owner
	}
	if !f.unattached[mediaID] {
		return appapiary.ErrImageNotFound // unknown, or not the caller's
	}
	delete(f.unattached, mediaID)
	f.attached[mediaID] = apiaryID
	return nil
}

func (f *fakeMediaDeleter) Detach(_ context.Context, _ string, mediaID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.attached, mediaID)
	return nil
}

// newService builds a Service backed by repo, with always-succeeding fake
// hive/media deleters - the right default for every test that isn't
// specifically exercising the delete cascade.
func newService(repo *fakeRepo) *appapiary.Service {
	return appapiary.NewService(repo, newFakeHiveDeleter(), newFakeMediaDeleter())
}

// --- tests ---

func TestCreate_Success(t *testing.T) {
	svc := newService(newFakeRepo())
	userID := uuid.New()

	a, err := svc.Create(context.Background(), userID, appapiary.CreateInput{
		Name:        "Home apiary",
		Location:    "Backyard",
		Description: "Two hives near the fence",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.UserID != userID {
		t.Errorf("UserID = %s, want %s", a.UserID, userID)
	}
	if a.Name != "Home apiary" {
		t.Errorf("Name = %q, want %q", a.Name, "Home apiary")
	}
}

func TestCreate_WithCoordinates(t *testing.T) {
	svc := newService(newFakeRepo())
	userID := uuid.New()
	lat, lon := 45.5, -122.6

	a, err := svc.Create(context.Background(), userID, appapiary.CreateInput{
		Name: "Home apiary",
		Lat:  &lat,
		Lon:  &lon,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Lat == nil || *a.Lat != lat {
		t.Errorf("Lat = %v, want %v", a.Lat, lat)
	}
	if a.Lon == nil || *a.Lon != lon {
		t.Errorf("Lon = %v, want %v", a.Lon, lon)
	}
}

func TestCreate_WithoutCoordinates(t *testing.T) {
	svc := newService(newFakeRepo())
	userID := uuid.New()

	a, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Lat != nil || a.Lon != nil {
		t.Errorf("Lat/Lon = %v/%v, want nil/nil", a.Lat, a.Lon)
	}
}

func TestGet_Success(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "A"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _, err := svc.Get(context.Background(), userID, "token", created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get returned %s, want %s", got.ID, created.ID)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newService(newFakeRepo())

	_, _, err := svc.Get(context.Background(), uuid.New(), "token", uuid.New())
	if !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get with unknown id: got %v, want ErrNotFound", err)
	}
}

// TestGet_WrongOwner_ReturnsNotFound is the core security guarantee of
// this module: a user must never be able to access another user's
// apiary, and specifically must not be able to distinguish "doesn't
// exist" from "exists but isn't mine".
func TestGet_WrongOwner_ReturnsNotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	owner := uuid.New()
	other := uuid.New()

	created, err := svc.Create(context.Background(), owner, appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, _, err = svc.Get(context.Background(), other, "token", created.ID)
	if !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get by non-owner: got %v, want ErrNotFound", err)
	}
}

func TestList_ReturnsOnlyOwnApiaries(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userA := uuid.New()
	userB := uuid.New()

	if _, err := svc.Create(context.Background(), userA, appapiary.CreateInput{Name: "A1"}); err != nil {
		t.Fatalf("Create A1: %v", err)
	}
	if _, err := svc.Create(context.Background(), userA, appapiary.CreateInput{Name: "A2"}); err != nil {
		t.Fatalf("Create A2: %v", err)
	}
	if _, err := svc.Create(context.Background(), userB, appapiary.CreateInput{Name: "B1"}); err != nil {
		t.Fatalf("Create B1: %v", err)
	}

	list, total, err := svc.List(context.Background(), userA, pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 {
		t.Fatalf("List total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Fatalf("List returned %d apiaries, want 2", len(list))
	}
	for _, a := range list {
		if a.UserID != userA {
			t.Errorf("List leaked apiary %s owned by %s into userA's list", a.ID, a.UserID)
		}
	}
}

func TestList_Pagination(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userID := uuid.New()

	for i := 0; i < 5; i++ {
		if _, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "A"}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	firstPage, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 1, Limit: 2})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(firstPage) != 2 {
		t.Fatalf("page 1 returned %d apiaries, want 2", len(firstPage))
	}

	lastPage, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 3, Limit: 2})
	if err != nil {
		t.Fatalf("List page 3: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(lastPage) != 1 {
		t.Fatalf("page 3 returned %d apiaries, want 1", len(lastPage))
	}

	beyond, total, err := svc.List(context.Background(), userID, pagination.Params{Page: 10, Limit: 2})
	if err != nil {
		t.Fatalf("List page 10: %v", err)
	}
	if total != 5 {
		t.Fatalf("total = %d, want 5", total)
	}
	if len(beyond) != 0 {
		t.Fatalf("page beyond available data returned %d apiaries, want 0", len(beyond))
	}
}

func TestList_Empty(t *testing.T) {
	svc := newService(newFakeRepo())

	list, total, err := svc.List(context.Background(), uuid.New(), pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	if len(list) != 0 {
		t.Fatalf("List = %v, want empty", list)
	}
}

func TestUpdate_Success(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Old name"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, _, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:        "New name",
		Location:    "New location",
		Description: "New description",
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "New name" {
		t.Errorf("Name = %q, want %q", updated.Name, "New name")
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) && updated.UpdatedAt != created.UpdatedAt {
		t.Errorf("UpdatedAt did not advance")
	}
}

func TestUpdate_WrongOwner_ReturnsNotFound(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	owner := uuid.New()
	other := uuid.New()

	created, err := svc.Create(context.Background(), owner, appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, _, err = svc.Update(context.Background(), other, "token", created.ID, appapiary.UpdateInput{Name: "Hijacked"})
	if !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Update by non-owner: got %v, want ErrNotFound", err)
	}

	// and the original must be untouched
	got, _, err := svc.Get(context.Background(), owner, "token", created.ID)
	if err != nil {
		t.Fatalf("Get after failed hijack attempt: %v", err)
	}
	if got.Name != "Owner's apiary" {
		t.Errorf("Name = %q after failed hijack attempt, want unchanged %q", got.Name, "Owner's apiary")
	}
}

// TestUpdate_ImagesNil_LeavesAttachedMediaUntouched proves that omitting
// Images from an update (the nil case) is a no-op on media-service: a
// client updating just the name must never accidentally detach every
// photo.
func TestUpdate_ImagesNil_LeavesAttachedMediaUntouched(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mediaID := uuid.New()
	media.attach(created.ID, mediaID)

	_, images, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{Name: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(images) != 1 || images[0] != mediaID {
		t.Fatalf("images = %v, want [%s] (untouched)", images, mediaID)
	}
}

// TestUpdate_ImagesEmpty_DetachesEverything proves that explicitly
// sending an empty images list (as opposed to omitting the field) clears
// every attached media item.
func TestUpdate_ImagesEmpty_DetachesEverything(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mediaID := uuid.New()
	media.attach(created.ID, mediaID)

	empty := []uuid.UUID{}
	_, images, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &empty,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(images) != 0 {
		t.Fatalf("images = %v, want empty", images)
	}
	if _, ok := media.attached[mediaID]; ok {
		t.Error("media should have been detached")
	}
}

// TestUpdate_ImagesPrunesUnwanted proves that an update whose images list
// keeps only some of the currently attached media detaches the rest,
// without erroring on the ones that stay.
func TestUpdate_ImagesPrunesUnwanted(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	keep := uuid.New()
	drop := uuid.New()
	media.attach(created.ID, keep)
	media.attach(created.ID, drop)

	desired := []uuid.UUID{keep, keep} // duplicated on purpose
	_, images, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(images) != 1 || images[0] != keep {
		t.Fatalf("images = %v, want [%s] deduplicated", images, keep)
	}
	if _, ok := media.attached[drop]; ok {
		t.Error("drop should have been detached")
	}
	if _, ok := media.attached[keep]; !ok {
		t.Error("keep should still be attached")
	}
}

// TestUpdate_ImagesRejectsForeignMedia proves that an update can't
// attach a media ID that isn't already this apiary's own media - one
// belonging to a different apiary (even the same user's) is rejected,
// and, critically, no detach happens and the apiary's other fields are
// left unchanged.
func TestUpdate_ImagesRejectsForeignMedia(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	kept := uuid.New()
	media.attach(created.ID, kept)

	otherApiary := uuid.New()
	foreign := uuid.New()
	media.attach(otherApiary, foreign)

	desired := []uuid.UUID{foreign}
	_, _, err = svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if !errors.Is(err, appapiary.ErrImageNotFound) {
		t.Fatalf("Update with foreign media: got %v, want ErrImageNotFound", err)
	}

	if _, ok := media.attached[kept]; !ok {
		t.Error("existing media must not be detached when validation fails first")
	}
	got, err := repo.GetByID(context.Background(), userID, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Home apiary" {
		t.Errorf("Name = %q after rejected update, want unchanged %q", got.Name, "Home apiary")
	}
}

// TestUpdate_ImagesAttachesFreshUpload proves media-service's decoupled
// upload flow end to end from apiary-service's side: an ID for media the
// caller uploaded but never attached to anything can be named in
// images and gets linked to this apiary, not just IDs already attached
// here.
func TestUpdate_ImagesAttachesFreshUpload(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fresh := uuid.New()
	media.uploadUnattached(fresh)

	desired := []uuid.UUID{fresh}
	_, images, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(images) != 1 || images[0] != fresh {
		t.Fatalf("images = %v, want [%s]", images, fresh)
	}
	if owner, ok := media.attached[fresh]; !ok || owner != created.ID {
		t.Errorf("fresh upload was not attached to the apiary: attached=%v ok=%v", owner, ok)
	}
}

func TestDelete_Success(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Gone soon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, "token", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, _, err := svc.Get(context.Background(), userID, "token", created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestDelete_WrongOwner_ReturnsNotFoundAndDoesNotDelete(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	owner := uuid.New()
	other := uuid.New()

	created, err := svc.Create(context.Background(), owner, appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), other, "attacker-token", created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Delete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, _, err := svc.Get(context.Background(), owner, "token", created.ID); err != nil {
		t.Fatalf("owner's apiary should survive a failed delete attempt by another user: %v", err)
	}
}

func TestDelete_CascadesHivesAndMediaBeforeApiary(t *testing.T) {
	repo := newFakeRepo()
	hives := newFakeHiveDeleter()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, hives, media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Gone soon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, "token", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !hives.wasDeleted(created.ID) {
		t.Error("Delete did not cascade to hive-service")
	}
	if !media.wasDeleted(created.ID) {
		t.Error("Delete did not cascade to media-service")
	}
	if _, _, err := svc.Get(context.Background(), userID, "token", created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestDelete_AbortsOnHiveCascadeFailure_ApiarySurvives is the core
// abort-on-failure guarantee: if hive-service can't be reached (or fails
// for any other reason), the apiary itself must not be deleted -
// otherwise its hives (and their inspections/media) would be permanently
// orphaned.
func TestDelete_AbortsOnHiveCascadeFailure_ApiarySurvives(t *testing.T) {
	repo := newFakeRepo()
	hives := newFakeHiveDeleter()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, hives, media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Survives"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("hive-service unreachable")
	hives.failOn(created.ID, boom)

	if err := svc.Delete(context.Background(), userID, "token", created.ID); !errors.Is(err, boom) {
		t.Fatalf("Delete: got %v, want %v", err, boom)
	}

	if media.wasDeleted(created.ID) {
		t.Error("media-service was called even though hive-service failed first")
	}
	if _, _, err := svc.Get(context.Background(), userID, "token", created.ID); err != nil {
		t.Fatalf("apiary should survive when hive-service fails: %v", err)
	}
}

// TestDelete_AbortsOnMediaDeleteFailure_ApiarySurvives mirrors the
// previous test for the second cascade step: media-service failing must
// also stop the apiary itself from being deleted, even though
// hive-service's step already succeeded.
func TestDelete_AbortsOnMediaDeleteFailure_ApiarySurvives(t *testing.T) {
	repo := newFakeRepo()
	hives := newFakeHiveDeleter()
	media := newFakeMediaDeleter()
	svc := appapiary.NewService(repo, hives, media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Survives"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("media-service unreachable")
	media.failOn(created.ID, boom)

	if err := svc.Delete(context.Background(), userID, "token", created.ID); !errors.Is(err, boom) {
		t.Fatalf("Delete: got %v, want %v", err, boom)
	}

	if !hives.wasDeleted(created.ID) {
		t.Error("hive-service should have already been called before media-service failed")
	}
	if _, _, err := svc.Get(context.Background(), userID, "token", created.ID); err != nil {
		t.Fatalf("apiary should survive when media-service fails: %v", err)
	}
}
