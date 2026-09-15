//go:build integration

package postgres_test

// Verifies WritableIDs' deterministic (created_at, id) ordering against
// real PostgreSQL, including the tie-break needed when several rows share
// a timestamp (a real possibility from a batch insert) - the same
// property TestApiaryRepository_ListByUser_StableOrdering exercises for
// ListByUser.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	repopostgres "github.com/sbezhuk/beebase-apiary-service/internal/repository/postgres"
)

func TestApiaryRepository_WritableIDs_OldestFirst(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	oldest := apiary.New(userID, "Oldest", "", "")
	if err := repo.Create(ctx, oldest); err != nil {
		t.Fatalf("create oldest: %v", err)
	}
	middle := apiary.New(userID, "Middle", "", "")
	middle.CreatedAt = oldest.CreatedAt.Add(time.Hour)
	if err := repo.Create(ctx, middle); err != nil {
		t.Fatalf("create middle: %v", err)
	}
	newest := apiary.New(userID, "Newest", "", "")
	newest.CreatedAt = oldest.CreatedAt.Add(2 * time.Hour)
	if err := repo.Create(ctx, newest); err != nil {
		t.Fatalf("create newest: %v", err)
	}

	ids, err := repo.WritableIDs(ctx, userID, 2)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != oldest.ID || ids[1] != middle.ID {
		t.Fatalf("WritableIDs(limit=2) = %v, want [%s, %s]", ids, oldest.ID, middle.ID)
	}
}

// TestApiaryRepository_WritableIDs_TiedCreatedAt_StableByID proves the id
// tiebreaker keeps the selection deterministic even when several apiaries
// share the exact same created_at (a batch insert, say).
func TestApiaryRepository_WritableIDs_TiedCreatedAt_StableByID(t *testing.T) {
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
	ids := make([]uuid.UUID, 3)
	for i := range ids {
		a := apiary.New(userID, uuid.NewString(), "", "")
		a.CreatedAt = now
		a.UpdatedAt = now
		if err := repo.Create(ctx, a); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		ids[i] = a.ID
	}

	first, err := repo.WritableIDs(ctx, userID, 1)
	if err != nil {
		t.Fatalf("WritableIDs run 1: %v", err)
	}
	second, err := repo.WritableIDs(ctx, userID, 1)
	if err != nil {
		t.Fatalf("WritableIDs run 2: %v", err)
	}
	if len(first) != 1 || len(second) != 1 || first[0] != second[0] {
		t.Fatalf("WritableIDs is not stable across identical calls: %v vs %v", first, second)
	}
}

// TestApiaryRepository_WritableIDs_ExcludesDeleted proves a deleted
// apiary never occupies a writable slot, so its removal correctly frees
// one up for the next-oldest survivor.
func TestApiaryRepository_WritableIDs_ExcludesDeleted(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	repo := repopostgres.NewApiaryRepository(tx)
	userID := uuid.New()

	oldest := apiary.New(userID, "Oldest", "", "")
	if err := repo.Create(ctx, oldest); err != nil {
		t.Fatalf("create oldest: %v", err)
	}
	newer := apiary.New(userID, "Newer", "", "")
	newer.CreatedAt = oldest.CreatedAt.Add(time.Hour)
	if err := repo.Create(ctx, newer); err != nil {
		t.Fatalf("create newer: %v", err)
	}

	if err := repo.HardDelete(ctx, userID, oldest.ID); err != nil {
		t.Fatalf("HardDelete oldest: %v", err)
	}

	ids, err := repo.WritableIDs(ctx, userID, 1)
	if err != nil {
		t.Fatalf("WritableIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != newer.ID {
		t.Fatalf("WritableIDs after deleting the oldest = %v, want [%s] (automatic promotion)", ids, newer.ID)
	}
}
