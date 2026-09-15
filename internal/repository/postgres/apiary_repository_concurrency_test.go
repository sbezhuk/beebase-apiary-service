//go:build integration

package postgres_test

// Verifies CreateWithLimit's advisory-lock guard actually serializes
// concurrent creates against real PostgreSQL - a plain count-then-insert
// without it would let a burst of concurrent requests each observe
// "count < limit" before any of them commits, letting a Free user exceed
// their apiary quota. This exercises the real *pgxpool.Pool path (the
// tx-per-call codepath in CreateWithLimit), not the single-shared-tx
// pattern the rest of this file's tests use, since a shared tx can't
// simulate genuinely concurrent transactions or session-scoped advisory
// locks.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	repopostgres "github.com/sbezhuk/beebase-apiary-service/internal/repository/postgres"
)

func TestApiaryRepository_CreateWithLimit_ConcurrentCreatesDoNotExceedLimit(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	userID := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM apiaries WHERE user_id = $1`, userID)
	})

	repo := repopostgres.NewApiaryRepository(pool)

	const attempts = 10
	const limit = 1

	var wg sync.WaitGroup
	results := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := apiary.New(userID, fmt.Sprintf("Concurrent %d", i), "", "")
			results <- repo.CreateWithLimit(ctx, a, limit)
		}(i)
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, apiary.ErrLimitReached):
			// expected for every attempt beyond the limit
		default:
			t.Fatalf("unexpected error from a concurrent create: %v", err)
		}
	}
	if successes != limit {
		t.Fatalf("successful concurrent creates = %d, want exactly %d - a count-then-insert race would let this exceed the limit", successes, limit)
	}

	count, err := repo.CountByUser(ctx, userID)
	if err != nil {
		t.Fatalf("CountByUser: %v", err)
	}
	if count != limit {
		t.Fatalf("final apiary count = %d, want %d", count, limit)
	}
}
