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

// fakeMediaClient stands in for application/apiary.MediaClient:
// ownedIDs is the set of media ids VerifyOwnership will accept as
// belonging to the caller (seeded via own()), and deletedIDs records
// every id ever passed to DeleteByIDs, in calls not suppressed by
// failDeleteWith.
type fakeMediaClient struct {
	mu         sync.Mutex
	ownedIDs   map[uuid.UUID]bool
	deletedIDs []uuid.UUID
	deleteErr  error
}

func newFakeMediaClient() *fakeMediaClient {
	return &fakeMediaClient{ownedIDs: map[uuid.UUID]bool{}}
}

// own registers each of ids as belonging to the caller, so VerifyOwnership
// accepts it.
func (f *fakeMediaClient) own(ids ...uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		f.ownedIDs[id] = true
	}
}

// failDeleteWith makes every subsequent DeleteByIDs call fail with err,
// simulating media-service being unreachable.
func (f *fakeMediaClient) failDeleteWith(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteErr = err
}

func (f *fakeMediaClient) VerifyOwnership(_ context.Context, _ string, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		if !f.ownedIDs[id] {
			return appapiary.ErrImageNotFound
		}
	}
	return nil
}

func (f *fakeMediaClient) DeleteByIDs(_ context.Context, _ string, ids []uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deletedIDs = append(f.deletedIDs, ids...)
	return nil
}

func (f *fakeMediaClient) wasDeleted(id uuid.UUID) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.deletedIDs {
		if d == id {
			return true
		}
	}
	return false
}

func (f *fakeMediaClient) deleteCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deletedIDs)
}

// newService builds a Service backed by repo, with always-succeeding fake
// hive/media clients - the right default for every test that isn't
// specifically exercising the delete cascade or images.
func newService(repo *fakeRepo) *appapiary.Service {
	return appapiary.NewService(repo, newFakeHiveDeleter(), newFakeMediaClient())
}

// --- tests ---

func TestCreate_Success(t *testing.T) {
	svc := newService(newFakeRepo())
	userID := uuid.New()

	a, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
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
	if len(a.Images) != 0 {
		t.Errorf("Images = %v, want empty", a.Images)
	}
}

func TestCreate_WithCoordinates(t *testing.T) {
	svc := newService(newFakeRepo())
	userID := uuid.New()
	lat, lon := 45.5, -122.6

	a, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
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

	a, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if a.Lat != nil || a.Lon != nil {
		t.Errorf("Lat/Lon = %v/%v, want nil/nil", a.Lat, a.Lon)
	}
}

// TestCreate_WithImages_Success proves an apiary can be created with
// photos already referenced, without a separate PUT: ownership of every
// id is verified against media-service before the apiary is persisted.
func TestCreate_WithImages_Success(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()
	photo1 := uuid.New()
	photo2 := uuid.New()
	media.own(photo1, photo2)

	a, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Home apiary",
		Images: []uuid.UUID{photo1, photo2, photo1}, // duplicated on purpose
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(a.Images) != 2 || a.Images[0] != photo1 || a.Images[1] != photo2 {
		t.Fatalf("Images = %v, want [%s, %s] deduplicated", a.Images, photo1, photo2)
	}

	got, err := repo.GetByID(context.Background(), userID, a.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Images) != 2 {
		t.Fatalf("persisted Images = %v, want 2 entries", got.Images)
	}
}

// TestCreate_WithImages_RejectsForeignMedia proves Create validates
// ownership of every referenced image before persisting anything - since
// verification happens first, a rejected image means no apiary is ever
// created (no rollback needed, unlike the old attach-after-insert design).
func TestCreate_WithImages_RejectsForeignMedia(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaClient() // foreign is deliberately never own()'d
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()
	foreign := uuid.New()

	_, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Home apiary",
		Images: []uuid.UUID{foreign},
	})
	if !errors.Is(err, appapiary.ErrImageNotFound) {
		t.Fatalf("Create with foreign media: got %v, want ErrImageNotFound", err)
	}

	list, _, err := repo.ListByUser(context.Background(), userID, pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("apiary was persisted despite a rejected image: %v", list)
	}
}

func TestGet_Success(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "A"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Get(context.Background(), userID, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("Get returned %s, want %s", got.ID, created.ID)
	}
}

func TestGet_NotFound(t *testing.T) {
	svc := newService(newFakeRepo())

	_, err := svc.Get(context.Background(), uuid.New(), uuid.New())
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

	created, err := svc.Create(context.Background(), owner, "token", appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Get(context.Background(), other, created.ID)
	if !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get by non-owner: got %v, want ErrNotFound", err)
	}
}

func TestList_ReturnsOnlyOwnApiaries(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userA := uuid.New()
	userB := uuid.New()

	if _, err := svc.Create(context.Background(), userA, "token", appapiary.CreateInput{Name: "A1"}); err != nil {
		t.Fatalf("Create A1: %v", err)
	}
	if _, err := svc.Create(context.Background(), userA, "token", appapiary.CreateInput{Name: "A2"}); err != nil {
		t.Fatalf("Create A2: %v", err)
	}
	if _, err := svc.Create(context.Background(), userB, "token", appapiary.CreateInput{Name: "B1"}); err != nil {
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
		if _, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "A"}); err != nil {
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

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "Old name"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
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

	created, err := svc.Create(context.Background(), owner, "token", appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Update(context.Background(), other, "token", created.ID, appapiary.UpdateInput{Name: "Hijacked"})
	if !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Update by non-owner: got %v, want ErrNotFound", err)
	}

	// and the original must be untouched
	got, err := svc.Get(context.Background(), owner, created.ID)
	if err != nil {
		t.Fatalf("Get after failed hijack attempt: %v", err)
	}
	if got.Name != "Owner's apiary" {
		t.Errorf("Name = %q after failed hijack attempt, want unchanged %q", got.Name, "Owner's apiary")
	}
}

// TestUpdate_ImagesNil_LeavesImagesUntouched proves that omitting Images
// from an update (the nil case) is a no-op: a client updating just the
// name must never accidentally clear every photo reference.
func TestUpdate_ImagesNil_LeavesImagesUntouched(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()
	mediaID := uuid.New()
	media.own(mediaID)

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Home apiary",
		Images: []uuid.UUID{mediaID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{Name: "Renamed"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != mediaID {
		t.Fatalf("Images = %v, want [%s] (untouched)", updated.Images, mediaID)
	}
}

// TestUpdate_ImagesEmpty_ClearsReferencesWithoutDeletingFiles proves that
// explicitly sending an empty images list (as opposed to omitting the
// field) clears the apiary's reference list - but, critically, does NOT
// delete the underlying media file: removing a reference and deleting a
// file are two separate actions now (the file stays until something
// explicitly calls DELETE /media/{id}, or the whole apiary is deleted).
func TestUpdate_ImagesEmpty_ClearsReferencesWithoutDeletingFiles(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()
	mediaID := uuid.New()
	media.own(mediaID)

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Home apiary",
		Images: []uuid.UUID{mediaID},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	empty := []uuid.UUID{}
	updated, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &empty,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 0 {
		t.Fatalf("Images = %v, want empty", updated.Images)
	}
	if media.wasDeleted(mediaID) {
		t.Error("removing an image reference must not delete the underlying file")
	}
}

// TestUpdate_ImagesReplacedWholesale proves an update's images list fully
// replaces the previous one (deduplicated) - the dropped id's file
// survives untouched, since remove-from-apiary and delete-the-file are
// separate actions now.
func TestUpdate_ImagesReplacedWholesale(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()
	keep := uuid.New()
	drop := uuid.New()
	media.own(keep, drop)

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Home apiary",
		Images: []uuid.UUID{keep, drop},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	desired := []uuid.UUID{keep, keep} // duplicated on purpose
	updated, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != keep {
		t.Fatalf("Images = %v, want [%s] deduplicated", updated.Images, keep)
	}
	if media.wasDeleted(drop) {
		t.Error("drop's file must survive - dropping a reference doesn't delete it")
	}
}

// TestUpdate_ImagesRejectsForeignMedia proves that an update can't
// reference a media id that isn't the caller's own, and, critically,
// leaves the apiary's images and other fields completely unchanged.
func TestUpdate_ImagesRejectsForeignMedia(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaClient() // foreign is deliberately never own()'d
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()
	kept := uuid.New()
	media.own(kept)

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Home apiary",
		Images: []uuid.UUID{kept},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	foreign := uuid.New()
	desired := []uuid.UUID{foreign}
	_, err = svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if !errors.Is(err, appapiary.ErrImageNotFound) {
		t.Fatalf("Update with foreign media: got %v, want ErrImageNotFound", err)
	}

	got, err := repo.GetByID(context.Background(), userID, created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Home apiary" {
		t.Errorf("Name = %q after rejected update, want unchanged %q", got.Name, "Home apiary")
	}
	if len(got.Images) != 1 || got.Images[0] != kept {
		t.Errorf("Images = %v after rejected update, want unchanged [%s]", got.Images, kept)
	}
}

// TestUpdate_ImagesAcceptsNewlyOwnedMedia proves an id the caller uploaded
// after this apiary was created can be added via a plain Update, once
// media-service confirms it belongs to them.
func TestUpdate_ImagesAcceptsNewlyOwnedMedia(t *testing.T) {
	repo := newFakeRepo()
	media := newFakeMediaClient()
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "Home apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	fresh := uuid.New()
	media.own(fresh)

	desired := []uuid.UUID{fresh}
	updated, err := svc.Update(context.Background(), userID, "token", created.ID, appapiary.UpdateInput{
		Name:   "Renamed",
		Images: &desired,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != fresh {
		t.Fatalf("Images = %v, want [%s]", updated.Images, fresh)
	}
}

func TestDelete_Success(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "Gone soon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, "token", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Get(context.Background(), userID, created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestDelete_WrongOwner_ReturnsNotFoundAndDoesNotDelete(t *testing.T) {
	repo := newFakeRepo()
	svc := newService(repo)
	owner := uuid.New()
	other := uuid.New()

	created, err := svc.Create(context.Background(), owner, "token", appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), other, "attacker-token", created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Delete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := svc.Get(context.Background(), owner, created.ID); err != nil {
		t.Fatalf("owner's apiary should survive a failed delete attempt by another user: %v", err)
	}
}

// TestDelete_CascadesHivesAndImagesBeforeApiary proves the full cascade:
// hive-service is asked to delete every hive first, then every media file
// this apiary itself references is hard-deleted, then the apiary row
// itself.
func TestDelete_CascadesHivesAndImagesBeforeApiary(t *testing.T) {
	repo := newFakeRepo()
	hives := newFakeHiveDeleter()
	media := newFakeMediaClient()
	svc := appapiary.NewService(repo, hives, media)
	userID := uuid.New()
	photo := uuid.New()
	media.own(photo)

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Gone soon",
		Images: []uuid.UUID{photo},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, "token", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !hives.wasDeleted(created.ID) {
		t.Error("Delete did not cascade to hive-service")
	}
	if !media.wasDeleted(photo) {
		t.Error("Delete did not cascade to media-service for the apiary's own image")
	}
	if _, err := svc.Get(context.Background(), userID, created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

// TestDelete_SkipsMediaCallWhenNoImages proves Delete never bothers
// calling media-service for an apiary with no images - even one
// configured to fail, proving the call is genuinely skipped, not just
// coincidentally successful.
func TestDelete_SkipsMediaCallWhenNoImages(t *testing.T) {
	repo := newFakeRepo()
	hives := newFakeHiveDeleter()
	media := newFakeMediaClient()
	media.failDeleteWith(errors.New("should never be called"))
	svc := appapiary.NewService(repo, hives, media)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "No photos"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, "token", created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if media.deleteCallCount() != 0 {
		t.Error("DeleteByIDs was called even though the apiary had no images")
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
	media := newFakeMediaClient()
	svc := appapiary.NewService(repo, hives, media)
	userID := uuid.New()
	photo := uuid.New()
	media.own(photo)

	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Survives",
		Images: []uuid.UUID{photo},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("hive-service unreachable")
	hives.failOn(created.ID, boom)

	if err := svc.Delete(context.Background(), userID, "token", created.ID); !errors.Is(err, boom) {
		t.Fatalf("Delete: got %v, want %v", err, boom)
	}

	if media.deleteCallCount() != 0 {
		t.Error("media-service was called even though hive-service failed first")
	}
	if _, err := svc.Get(context.Background(), userID, created.ID); err != nil {
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
	media := newFakeMediaClient()
	userID := uuid.New()
	photo := uuid.New()
	media.own(photo)

	svc := appapiary.NewService(repo, hives, media)
	created, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{
		Name:   "Survives",
		Images: []uuid.UUID{photo},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	boom := errors.New("media-service unreachable")
	media.failDeleteWith(boom)

	if err := svc.Delete(context.Background(), userID, "token", created.ID); !errors.Is(err, boom) {
		t.Fatalf("Delete: got %v, want %v", err, boom)
	}

	if !hives.wasDeleted(created.ID) {
		t.Error("hive-service should have already been called before media-service failed")
	}
	if _, err := svc.Get(context.Background(), userID, created.ID); err != nil {
		t.Fatalf("apiary should survive when media-service fails: %v", err)
	}
}
