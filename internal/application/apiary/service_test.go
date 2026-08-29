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

func (f *fakeRepo) Delete(_ context.Context, userID, apiaryID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.byID[apiaryID]
	if !ok || a.UserID != userID || a.DeletedAt != nil {
		return apiary.ErrNotFound
	}
	now := a.UpdatedAt
	a.DeletedAt = &now
	return nil
}

// --- tests ---

func TestCreate_Success(t *testing.T) {
	svc := appapiary.NewService(newFakeRepo())
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
	svc := appapiary.NewService(newFakeRepo())
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
	svc := appapiary.NewService(newFakeRepo())
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
	svc := appapiary.NewService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "A"})
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
	svc := appapiary.NewService(newFakeRepo())

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
	svc := appapiary.NewService(repo)
	owner := uuid.New()
	other := uuid.New()

	created, err := svc.Create(context.Background(), owner, appapiary.CreateInput{Name: "Owner's apiary"})
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
	svc := appapiary.NewService(repo)
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
	svc := appapiary.NewService(repo)
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
	svc := appapiary.NewService(newFakeRepo())

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
	svc := appapiary.NewService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Old name"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated, err := svc.Update(context.Background(), userID, created.ID, appapiary.UpdateInput{
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
	svc := appapiary.NewService(repo)
	owner := uuid.New()
	other := uuid.New()

	created, err := svc.Create(context.Background(), owner, appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = svc.Update(context.Background(), other, created.ID, appapiary.UpdateInput{Name: "Hijacked"})
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

func TestDelete_Success(t *testing.T) {
	repo := newFakeRepo()
	svc := appapiary.NewService(repo)
	userID := uuid.New()

	created, err := svc.Create(context.Background(), userID, appapiary.CreateInput{Name: "Gone soon"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), userID, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := svc.Get(context.Background(), userID, created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Get after Delete: got %v, want ErrNotFound", err)
	}
}

func TestDelete_WrongOwner_ReturnsNotFoundAndDoesNotDelete(t *testing.T) {
	repo := newFakeRepo()
	svc := appapiary.NewService(repo)
	owner := uuid.New()
	other := uuid.New()

	created, err := svc.Create(context.Background(), owner, appapiary.CreateInput{Name: "Owner's apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := svc.Delete(context.Background(), other, created.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Delete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := svc.Get(context.Background(), owner, created.ID); err != nil {
		t.Fatalf("owner's apiary should survive a failed delete attempt by another user: %v", err)
	}
}
