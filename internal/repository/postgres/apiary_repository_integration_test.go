//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	repopostgres "github.com/sbezhuk/beebase-apiary-service/internal/repository/postgres"
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

	list, err := repo.ListByUser(ctx, userA)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
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

func TestApiaryRepository_Delete_SoftDelete(t *testing.T) {
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

	if err := repo.Delete(ctx, userID, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := repo.GetByID(ctx, userID, a.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("GetByID after delete: got %v, want ErrNotFound", err)
	}

	// The row itself must still exist (soft delete), just filtered out by
	// deleted_at IS NULL. Verify directly against the row so this test
	// would fail if Delete ever became a hard DELETE.
	var deletedAt *string
	err = tx.QueryRow(ctx, "SELECT deleted_at::text FROM apiaries WHERE id = $1", a.ID).Scan(&deletedAt)
	if err != nil {
		t.Fatalf("query raw row: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at is NULL after Delete; expected it to be set (soft delete)")
	}
}

func TestApiaryRepository_Delete_WrongOwner_NotFoundAndNotDeleted(t *testing.T) {
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

	if err := repo.Delete(ctx, other, a.ID); !errors.Is(err, apiary.ErrNotFound) {
		t.Fatalf("Delete by non-owner: got %v, want ErrNotFound", err)
	}

	if _, err := repo.GetByID(ctx, owner, a.ID); err != nil {
		t.Fatalf("owner's apiary should survive a failed delete attempt: %v", err)
	}
}
