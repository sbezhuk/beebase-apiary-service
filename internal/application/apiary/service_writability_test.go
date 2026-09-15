package apiary_test

// This file covers the dynamic Free/Pro writability model: which apiary
// remains writable under Free, how that selection reacts to deletes and
// re-upgrades, and the account-wide GET /apiaries/writable endpoint
// hive-service depends on. See application/apiary.Service.isWritable and
// Service.WritableApiaryID.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	appapiary "github.com/sbezhuk/beebase-apiary-service/internal/application/apiary"
	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
	"github.com/sbezhuk/beebase-common/pagination"
)

func newFreeService(repo *fakeRepo) *appapiary.Service {
	subs := &fakeSubscriptionClient{entitlement: appapiary.EntitlementFree}
	return appapiary.NewService(repo, newFakeHiveDeleter(), newFakeMediaClient(), subs)
}

// TestWritability_WithinFreeLimit_AllWritable is Case A: a Free user with
// only one apiary keeps it fully editable.
func TestWritability_WithinFreeLimit_AllWritable(t *testing.T) {
	repo := newFakeRepo()
	svc := newFreeService(repo)
	userID := uuid.New()

	a, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "Only apiary"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !a.Writable {
		t.Fatal("a single Free apiary should be writable")
	}

	got, err := svc.Get(context.Background(), userID, "token", a.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Writable {
		t.Error("Get: writable = false, want true")
	}
}

// TestWritability_OverFreeLimit_OldestWritable is Case B/C's apiary half:
// with two apiaries under Free, only the older one (by created_at, id) is
// writable - deterministic, per the product spec.
func TestWritability_OverFreeLimit_OldestWritable(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	older := apiary.New(userID, "Older", "", "")
	newer := apiary.New(userID, "Newer", "", "")
	newer.CreatedAt = older.CreatedAt.Add(time.Hour)
	mustCreate(t, repo, older)
	mustCreate(t, repo, newer)

	svc := newFreeService(repo)

	gotOlder, err := svc.Get(context.Background(), userID, "token", older.ID)
	if err != nil {
		t.Fatalf("Get older: %v", err)
	}
	if !gotOlder.Writable {
		t.Error("older apiary should be writable")
	}

	gotNewer, err := svc.Get(context.Background(), userID, "token", newer.ID)
	if err != nil {
		t.Fatalf("Get newer: %v", err)
	}
	if gotNewer.Writable {
		t.Error("newer apiary should be read-only")
	}
}

// TestWritability_DeleteWritableApiary_PromotesNextAutomatically is the
// task's explicit scenario: deleting the writable apiary must
// automatically make the next-oldest one writable, with no activation
// step - because writability is derived fresh from live data on every
// call, not from any persisted lock.
func TestWritability_DeleteWritableApiary_PromotesNextAutomatically(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	first := apiary.New(userID, "First", "", "")
	second := apiary.New(userID, "Second", "", "")
	second.CreatedAt = first.CreatedAt.Add(time.Hour)
	mustCreate(t, repo, first)
	mustCreate(t, repo, second)

	svc := newFreeService(repo)

	gotSecond, err := svc.Get(context.Background(), userID, "token", second.ID)
	if err != nil {
		t.Fatalf("Get second: %v", err)
	}
	if gotSecond.Writable {
		t.Fatal("second apiary should start read-only")
	}

	if err := svc.Delete(context.Background(), userID, "token", first.ID); err != nil {
		t.Fatalf("Delete first: %v", err)
	}

	gotSecond, err = svc.Get(context.Background(), userID, "token", second.ID)
	if err != nil {
		t.Fatalf("Get second after delete: %v", err)
	}
	if !gotSecond.Writable {
		t.Fatal("second apiary should automatically become writable once the first is deleted - no activation step should be needed")
	}
}

// TestWritability_ProBypassesEverything is Pro bypass: every apiary is
// writable, and Update against a normally-locked apiary succeeds.
func TestWritability_ProBypassesEverything(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	older := apiary.New(userID, "Older", "", "")
	newer := apiary.New(userID, "Newer", "", "")
	newer.CreatedAt = older.CreatedAt.Add(time.Hour)
	mustCreate(t, repo, older)
	mustCreate(t, repo, newer)

	svc := newService(repo) // Pro by default

	got, err := svc.Get(context.Background(), userID, "token", newer.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Writable {
		t.Fatal("under Pro every apiary should be writable")
	}

	if _, err := svc.Update(context.Background(), userID, "token", newer.ID, appapiary.UpdateInput{Name: "Renamed"}); err != nil {
		t.Fatalf("Update under Pro: %v", err)
	}
}

// TestWritability_ReupgradeImmediatelyUnlocks proves re-upgrading to Pro
// makes a previously read-only apiary writable on the very next request,
// with no migration or reconciliation step - the selection is recomputed
// from live data plus the caller's current entitlement on every call.
func TestWritability_ReupgradeImmediatelyUnlocks(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	older := apiary.New(userID, "Older", "", "")
	newer := apiary.New(userID, "Newer", "", "")
	newer.CreatedAt = older.CreatedAt.Add(time.Hour)
	mustCreate(t, repo, older)
	mustCreate(t, repo, newer)

	subs := &fakeSubscriptionClient{entitlement: appapiary.EntitlementFree}
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), newFakeMediaClient(), subs)

	got, err := svc.Get(context.Background(), userID, "token", newer.ID)
	if err != nil {
		t.Fatalf("Get while Free: %v", err)
	}
	if got.Writable {
		t.Fatal("newer apiary should start read-only under Free")
	}

	subs.entitlement = appapiary.EntitlementPro

	got, err = svc.Get(context.Background(), userID, "token", newer.ID)
	if err != nil {
		t.Fatalf("Get after re-upgrade: %v", err)
	}
	if !got.Writable {
		t.Fatal("newer apiary should be immediately writable after re-upgrading to Pro")
	}

	if _, err := svc.Update(context.Background(), userID, "token", newer.ID, appapiary.UpdateInput{Name: "Now editable"}); err != nil {
		t.Fatalf("Update after re-upgrade: %v", err)
	}
}

// TestUpdate_ReadOnlyApiary_Rejected proves Update independently enforces
// writability - not just Get's annotation - returning ErrReadOnly and
// leaving the apiary completely untouched.
func TestUpdate_ReadOnlyApiary_Rejected(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	older := apiary.New(userID, "Older", "", "")
	newer := apiary.New(userID, "Newer", "original location", "")
	newer.CreatedAt = older.CreatedAt.Add(time.Hour)
	mustCreate(t, repo, older)
	mustCreate(t, repo, newer)

	svc := newFreeService(repo)

	_, err := svc.Update(context.Background(), userID, "token", newer.ID, appapiary.UpdateInput{Name: "Hijacked"})
	if !errors.Is(err, appapiary.ErrReadOnly) {
		t.Fatalf("Update on read-only apiary: got %v, want ErrReadOnly", err)
	}

	got, err := repo.GetByID(context.Background(), userID, newer.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Location != "original location" {
		t.Errorf("Location = %q after rejected update, want unchanged", got.Location)
	}
}

// TestUpdate_ReadOnlyApiary_DeleteStillAllowed proves delete is never
// gated by writability - a Free user can always delete a locked apiary,
// which is how they'd shrink back under the limit or make room for
// something else.
func TestUpdate_ReadOnlyApiary_DeleteStillAllowed(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	older := apiary.New(userID, "Older", "", "")
	newer := apiary.New(userID, "Newer", "", "")
	newer.CreatedAt = older.CreatedAt.Add(time.Hour)
	mustCreate(t, repo, older)
	mustCreate(t, repo, newer)

	svc := newFreeService(repo)

	if err := svc.Delete(context.Background(), userID, "token", newer.ID); err != nil {
		t.Fatalf("Delete a read-only apiary should still be allowed: %v", err)
	}
}

// TestList_WritableFlag_IndependentOfPagination proves the writable
// apiary is determined from the caller's complete live apiaries, not from
// the current page - deleting the setup so the writable one only appears
// on page 2 must not change which apiary List reports as writable.
func TestList_WritableFlag_IndependentOfPagination(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	oldest := apiary.New(userID, "Oldest", "", "")
	mustCreate(t, repo, oldest)
	for i := 0; i < 3; i++ {
		later := apiary.New(userID, uuid.NewString(), "", "")
		later.CreatedAt = oldest.CreatedAt.Add(time.Duration(i+1) * time.Hour)
		mustCreate(t, repo, later)
	}

	svc := newFreeService(repo)

	// Page 1 (2 per page) contains the oldest apiary (page ordering is
	// created_at ASC by default).
	page1, _, err := svc.List(context.Background(), userID, "token", pagination.Params{Page: 1, Limit: 2}, nil, nil, false)
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	foundWritableOnPage1 := false
	for _, a := range page1 {
		if a.ID == oldest.ID && !a.Writable {
			t.Fatal("the oldest apiary must be reported writable regardless of which page it's read from")
		}
		if a.Writable {
			foundWritableOnPage1 = true
		}
	}
	if !foundWritableOnPage1 {
		t.Fatal("expected the writable apiary to appear on page 1")
	}

	// Every apiary on page 2 must be read-only - the writable slot was
	// already accounted for by the complete selection, not this page.
	page2, _, err := svc.List(context.Background(), userID, "token", pagination.Params{Page: 2, Limit: 2}, nil, nil, false)
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	for _, a := range page2 {
		if a.Writable {
			t.Errorf("apiary %s on page 2 reported writable; only the single oldest apiary should ever be", a.ID)
		}
	}
}

// TestWritableApiaryID_Free returns the sole writable apiary id for a Free
// user - the contract hive-service depends on to resolve parent-apiary
// writability in bulk.
func TestWritableApiaryID_Free(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	older := apiary.New(userID, "Older", "", "")
	newer := apiary.New(userID, "Newer", "", "")
	newer.CreatedAt = older.CreatedAt.Add(time.Hour)
	mustCreate(t, repo, older)
	mustCreate(t, repo, newer)

	svc := newFreeService(repo)

	id, unrestricted, err := svc.WritableApiaryID(context.Background(), userID, "token")
	if err != nil {
		t.Fatalf("WritableApiaryID: %v", err)
	}
	if unrestricted {
		t.Fatal("unrestricted = true for a Free user, want false")
	}
	if id == nil || *id != older.ID {
		t.Fatalf("WritableApiaryID = %v, want %s", id, older.ID)
	}
}

// TestWritableApiaryID_Pro reports unrestricted with no apiary id, since
// every apiary is writable and none is singled out.
func TestWritableApiaryID_Pro(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	svc := newService(repo) // Pro by default

	if _, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "A"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	id, unrestricted, err := svc.WritableApiaryID(context.Background(), userID, "token")
	if err != nil {
		t.Fatalf("WritableApiaryID: %v", err)
	}
	if !unrestricted {
		t.Fatal("unrestricted = false for a Pro user, want true")
	}
	if id != nil {
		t.Errorf("apiary id = %v, want nil under Pro", id)
	}
}

// TestWritableApiaryID_NoApiaries reports neither unrestricted nor an id
// for a Free user who owns no apiaries at all.
func TestWritableApiaryID_NoApiaries(t *testing.T) {
	svc := newFreeService(newFakeRepo())

	id, unrestricted, err := svc.WritableApiaryID(context.Background(), uuid.New(), "token")
	if err != nil {
		t.Fatalf("WritableApiaryID: %v", err)
	}
	if unrestricted {
		t.Fatal("unrestricted = true, want false")
	}
	if id != nil {
		t.Errorf("apiary id = %v, want nil", id)
	}
}

// TestWritability_MultipleSubscriptionCycles is Case G: Free -> Pro ->
// Free must not depend on remembered subscription history - only on
// current live apiaries at the moment of each check.
func TestWritability_MultipleSubscriptionCycles(t *testing.T) {
	repo := newFakeRepo()
	userID := uuid.New()
	subs := &fakeSubscriptionClient{entitlement: appapiary.EntitlementFree}
	svc := appapiary.NewService(repo, newFakeHiveDeleter(), newFakeMediaClient(), subs)

	// Free: create the one allowed apiary.
	a1, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "A1"})
	if err != nil {
		t.Fatalf("Create a1: %v", err)
	}

	// Upgrade to Pro: create two more. CreatedAt is forced strictly after
	// a1 (and after each other) since time.Now() alone doesn't guarantee
	// distinct timestamps for calls this close together, and the
	// (created_at, id) ordering this test exercises must be deterministic
	// - see TestWritability_OverFreeLimit_OldestWritable for the same
	// pattern.
	subs.entitlement = appapiary.EntitlementPro
	a2, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "A2"})
	if err != nil {
		t.Fatalf("Create a2: %v", err)
	}
	repo.byID[a2.ID].CreatedAt = a1.CreatedAt.Add(time.Hour)
	a3, err := svc.Create(context.Background(), userID, "token", appapiary.CreateInput{Name: "A3"})
	if err != nil {
		t.Fatalf("Create a3: %v", err)
	}
	repo.byID[a3.ID].CreatedAt = a1.CreatedAt.Add(2 * time.Hour)

	// Downgrade: only the oldest (a1) should remain writable.
	subs.entitlement = appapiary.EntitlementFree
	for id, want := range map[uuid.UUID]bool{a1.ID: true, a2.ID: false, a3.ID: false} {
		got, err := svc.Get(context.Background(), userID, "token", id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if got.Writable != want {
			t.Errorf("apiary %s writable = %v, want %v", id, got.Writable, want)
		}
	}

	// Delete a1 (the writable one) while still Free: a2, being the next
	// oldest of what remains, should now be writable - re-upgrading to
	// Pro is not required for this second downgrade's selection to be
	// correct, since it's recomputed from current data each time, not
	// from history of the first cycle.
	if err := svc.Delete(context.Background(), userID, "token", a1.ID); err != nil {
		t.Fatalf("Delete a1: %v", err)
	}
	got, err := svc.Get(context.Background(), userID, "token", a2.ID)
	if err != nil {
		t.Fatalf("Get a2: %v", err)
	}
	if !got.Writable {
		t.Fatal("a2 should become writable after a1 is deleted, across the second Free period")
	}
}
