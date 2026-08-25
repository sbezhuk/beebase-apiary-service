// Package apiary holds the Apiary entity and the port through which the
// rest of the application persists and retrieves it. It has no dependency
// on HTTP, PostgreSQL, or any other infrastructure concern.
package apiary

import (
	"time"

	"github.com/google/uuid"
)

// Apiary is a beekeeper's registered apiary: a physical site holding one
// or more hives. It is a synchronizable entity (UUID, created_at,
// updated_at, deleted_at) per the project's offline-sync plan, even
// though full sync isn't implemented yet.
type Apiary struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Name      string
	Location  string
	Notes     string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// New constructs an Apiary owned by userID, with a freshly generated ID
// and timestamps set to now.
func New(userID uuid.UUID, name, location, notes string) *Apiary {
	now := time.Now().UTC()
	return &Apiary{
		ID:        uuid.New(),
		UserID:    userID,
		Name:      name,
		Location:  location,
		Notes:     notes,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
