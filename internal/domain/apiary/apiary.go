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
	ID          uuid.UUID
	UserID      uuid.UUID
	Name        string
	Location    string
	Description string
	// Lat and Lon are the apiary's GPS coordinates. Both are optional
	// (nil when not set) and independent of Location, which is a free-text
	// description.
	Lat *float64
	Lon *float64
	// Images is the set of media ids attached to this apiary - the source
	// of truth for what's attached (nothing asks media-service on every
	// read). Never nil; empty when there are no photos.
	Images    []uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
}

// New constructs an Apiary owned by userID, with a freshly generated ID
// and timestamps set to now.
func New(userID uuid.UUID, name, location, description string) *Apiary {
	now := time.Now().UTC()
	return &Apiary{
		ID:          uuid.New(),
		UserID:      userID,
		Name:        name,
		Location:    location,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
