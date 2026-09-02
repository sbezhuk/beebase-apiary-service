//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	repopostgres "github.com/sbezhuk/beebase-apiary-service/internal/repository/postgres"
	"github.com/sbezhuk/beebase-common/pagination"
)

func TestApiaryRepository_CreateAndGet(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	a := apiary.New(userID, "Home apiary", "Backyard", "two hives")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.GetByID(ctx, userID, a.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != a.Name || got.Location != a.Location || got.Description != a.Description {
		t.Errorf("GetByID = %+v, want fields matching %+v", got, a)
	}
}

// TestApiaryRepository_ImagesRoundTripThroughCreateAndUpdate proves the
// images column - apiary-service's own source of truth for attached
// media, rather than a media-service round trip - survives Create,
// GetByID, and Update intact, including an apiary with none.
func TestApiaryRepository_ImagesRoundTripThroughCreateAndUpdate(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	withoutImages := apiary.New(userID, "No photos yet", "", "")
	if err := repo.Create(ctx, withoutImages); err != nil {
		t.Fatalf("Create without images: %v", err)
	}
	got, err := repo.GetByID(ctx, userID, withoutImages.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Images) != 0 {
		t.Fatalf("Images = %v, want empty (not null)", got.Images)
	}

	img1, img2 := uuid.New(), uuid.New()
	withImages := apiary.New(userID, "Has photos", "", "")
	withImages.Images = []uuid.UUID{img1, img2}
	if err := repo.Create(ctx, withImages); err != nil {
		t.Fatalf("Create with images: %v", err)
	}
	got, err = repo.GetByID(ctx, userID, withImages.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if len(got.Images) != 2 {
		t.Fatalf("Images = %v, want [%s, %s]", got.Images, img1, img2)
	}

	got.Images = []uuid.UUID{img1}
	got.UpdatedAt = time.Now().UTC()
	if err := repo.Update(ctx, got); err != nil {
		t.Fatalf("Update: %v", err)
	}
	updated, err := repo.GetByID(ctx, userID, withImages.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if len(updated.Images) != 1 || updated.Images[0] != img1 {
		t.Fatalf("Images after update = %v, want [%s]", updated.Images, img1)
	}
}

func TestApiaryRepository_GetByID_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)

	_, err = repo.GetByID(ctx, uuid.New(), uuid.New())
	if !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("GetByID for unknown apiary: got %v, want ErrNotFound", err)
	}
}

// TestApiaryRepository_GetByID_WrongOwner_NotFound is the real-database
// version of the module's central security guarantee: an apiary that
// exists, but belongs to someone else, must be indistinguishable from one
// that doesn't exist at all.
func TestApiaryRepository_GetByID_WrongOwner_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	owner := uuid.New()
	other := uuid.New()

	a := apiary.New(owner, "Owner's apiary", "", "")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = repo.GetByID(ctx, other, a.ID)
	if !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("GetByID by non-owner: got %v, want ErrNotFound", err)
	}
}

func TestApiaryRepository_ListByUser_OnlyOwnApiaries(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userA := uuid.New()
	userB := uuid.New()

	for _, name := range []string{"A1", "A2"} {
		if err := repo.Create(ctx, apiary.New(userA, name, "", "")); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	if err := repo.Create(ctx, apiary.New(userB, "B1", "", "")); err != nil {
		t.Fatalf("create B1: %v", err)
	}

	list, total, err := repo.ListByUser(ctx, userA, pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if total != 2 {
		t.Fatalf("ListByUser total = %d, want 2", total)
	}
	if len(list) != 2 {
		t.Fatalf("ListByUser returned %d apiaries, want 2", len(list))
	}
	for _, a := range list {
		if a.UserID != userA {
			t.Errorf("ListByUser leaked apiary %s owned by %s", a.ID, a.UserID)
		}
	}
}

func TestApiaryRepository_ListByUser_Pagination(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	const count = 5
	for i := 0; i < count; i++ {
		if err := repo.Create(ctx, apiary.New(userID, "A", "", "")); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}

	// First page.
	first, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 1: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(first) != 2 {
		t.Fatalf("page 1 returned %d apiaries, want 2", len(first))
	}

	// Middle page.
	middle, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 2, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 2: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(middle) != 2 {
		t.Fatalf("page 2 returned %d apiaries, want 2", len(middle))
	}

	// Last (partial) page.
	last, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 3, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 3: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(last) != 1 {
		t.Fatalf("page 3 returned %d apiaries, want 1", len(last))
	}

	// Page beyond available data.
	beyond, total, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 10, Limit: 2})
	if err != nil {
		t.Fatalf("ListByUser page 10: %v", err)
	}
	if total != count {
		t.Fatalf("total = %d, want %d", total, count)
	}
	if len(beyond) != 0 {
		t.Fatalf("page beyond available data returned %d apiaries, want 0", len(beyond))
	}

	// Pages must not overlap and together must cover every row exactly once.
	seen := map[uuid.UUID]bool{}
	for _, a := range append(append(first, middle...), last...) {
		if seen[a.ID] {
			t.Errorf("apiary %s appeared on more than one page", a.ID)
		}
		seen[a.ID] = true
	}
	if len(seen) != count {
		t.Errorf("pages together covered %d apiaries, want %d", len(seen), count)
	}
}

func TestApiaryRepository_ListByUser_Empty(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)

	list, total, err := repo.ListByUser(ctx, uuid.New(), pagination.Params{Page: 1, Limit: pagination.DefaultLimit})
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if total != 0 {
		t.Fatalf("total = %d, want 0", total)
	}
	if len(list) != 0 {
		t.Fatalf("ListByUser = %v, want empty", list)
	}
}

// TestApiaryRepository_ListByUser_StableOrdering guards against equal
// created_at timestamps reshuffling rows between pages: the id tiebreaker
// must make ordering deterministic even when many apiaries share a
// timestamp (a real possibility, since created_at defaults from the same
// batch insert).
func TestApiaryRepository_ListByUser_StableOrdering(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	now := time.Now().UTC()
	ids := make([]uuid.UUID, 4)
	for i := range ids {
		a := apiary.New(userID, "A", "", "")
		a.CreatedAt = now
		a.UpdatedAt = now
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids[i] = a.ID
	}

	firstRun, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 4})
	if err != nil {
		t.Fatalf("ListByUser run 1: %v", err)
	}
	secondRun, _, err := repo.ListByUser(ctx, userID, pagination.Params{Page: 1, Limit: 4})
	if err != nil {
		t.Fatalf("ListByUser run 2: %v", err)
	}

	if len(firstRun) != len(secondRun) {
		t.Fatalf("run lengths differ: %d vs %d", len(firstRun), len(secondRun))
	}
	for i := range firstRun {
		if firstRun[i].ID != secondRun[i].ID {
			t.Fatalf("ordering unstable at index %d: %s vs %s", i, firstRun[i].ID, secondRun[i].ID)
		}
	}
}

func TestApiaryRepository_Update(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	a := apiary.New(userID, "Old name", "Old location", "Old description")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	a.Name = "New name"
	a.Location = "New location"
	a.Description = "New description"
	if err := repo.Update(ctx, a); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := repo.GetByID(ctx, userID, a.ID)
	if err != nil {
		t.Fatalf("GetByID after update: %v", err)
	}
	if got.Name != "New name" || got.Location != "New location" || got.Description != "New description" {
		t.Errorf("GetByID after update = %+v, want updated fields", got)
	}
}

func TestApiaryRepository_Update_WrongOwner_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	owner := uuid.New()
	other := uuid.New()

	a := apiary.New(owner, "Owner's apiary", "", "")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	hijack := *a
	hijack.UserID = other
	hijack.Name = "Hijacked"
	if err := repo.Update(ctx, &hijack); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Update with mismatched owner: got %v, want ErrNotFound", err)
	}

	got, err := repo.GetByID(ctx, owner, a.ID)
	if err != nil {
		t.Fatalf("GetByID after failed hijack: %v", err)
	}
	if got.Name != "Owner's apiary" {
		t.Errorf("Name = %q after failed hijack, want unchanged", got.Name)
	}
}

func TestApiaryRepository_HardDelete_Success(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	a := apiary.New(userID, "Gone soon", "", "")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.HardDelete(ctx, userID, a.ID); err != nil {
		t.Fatalf("HardDelete: %v", err)
	}

	if _, err := repo.GetByID(ctx, userID, a.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("GetByID after HardDelete: got %v, want ErrNotFound", err)
	}

	// The row itself must be fully gone, not just deleted_at-marked.
	var n int
	if err := tx.QueryRow(ctx, "SELECT count(*) FROM apiaries WHERE id = $1", a.ID).Scan(&n); err != nil {
		t.Fatalf("raw count: %v", err)
	}
	if n != 0 {
		t.Errorf("apiary still present after HardDelete; want fully removed")
	}
}

func TestApiaryRepository_HardDelete_WrongOwner_NotFoundAndNotDeleted(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	owner := uuid.New()
	other := uuid.New()

	a := apiary.New(owner, "Owner's apiary", "", "")
	if err := repo.Create(ctx, a); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.HardDelete(ctx, other, a.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("HardDelete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := repo.GetByID(ctx, owner, a.ID); err != nil {
		t.Fatalf("owner's apiary should survive a failed delete attempt: %v", err)
	}
}
