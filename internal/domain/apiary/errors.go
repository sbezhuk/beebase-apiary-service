package apiary

import "errors"

// ErrNotFound is returned both when no apiary matches the given ID and
// when it exists but belongs to a different user. The two cases are
// deliberately indistinguishable to a caller: a user must never be able
// to tell whether another user's apiary ID exists at all.
var ErrNotFound = errors.New("apiary not found")

// ErrLimitReached is returned by Repository.CreateWithLimit when the user's
// active apiary count already meets or exceeds the specified limit.
var ErrLimitReached = errors.New("apiary limit reached")

// ErrNameTaken is returned when a user already has an active apiary with
// the same name.
var ErrNameTaken = errors.New("apiary name taken")
