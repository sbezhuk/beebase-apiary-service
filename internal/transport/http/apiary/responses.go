package apiary

import (
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
)

// Response is the public representation of an apiary.
type Response struct {
	ID          uuid.UUID   `json:"id"`
	Name        string      `json:"name"`
	Location    string      `json:"location"`
	Description string      `json:"description"`
	Lat         *float64    `json:"lat"`
	Lon         *float64    `json:"lon"`
	Images      []uuid.UUID `json:"images"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// newResponse builds a Response for a, with images as the IDs of media
// currently attached to it. A nil images (e.g. a freshly created apiary,
// or a list item that deliberately skips the media-service round trip)
// renders as "images": [] rather than null.
func newResponse(a *apiary.Apiary, images []uuid.UUID) Response {
	if images == nil {
		images = []uuid.UUID{}
	}
	return Response{
		ID:          a.ID,
		Name:        a.Name,
		Location:    a.Location,
		Description: a.Description,
		Lat:         a.Lat,
		Lon:         a.Lon,
		Images:      images,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

// newListResponse deliberately omits each item's attached media: fetching
// it would mean one media-service round trip per apiary in the page (up
// to MaxLimit), an N+1 fan-out this endpoint doesn't pay. Clients that
// need images for a listed apiary can fetch it directly via GET
// /apiaries/{id}, or query media-service's own list-by-owner endpoint.
func newListResponse(apiaries []*apiary.Apiary) []Response {
	out := make([]Response, len(apiaries))
	for i, a := range apiaries {
		out[i] = newResponse(a, nil)
	}
	return out
}
