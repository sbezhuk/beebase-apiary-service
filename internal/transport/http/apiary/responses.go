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

// newResponse builds a Response for a. Images is read straight from a -
// never nil (Apiary.Images is always a real, possibly-empty slice) - so
// it renders as "images": [] rather than null when there are no photos.
func newResponse(a *apiary.Apiary) Response {
	images := a.Images
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

func newListResponse(apiaries []*apiary.Apiary) []Response {
	out := make([]Response, len(apiaries))
	for i, a := range apiaries {
		out[i] = newResponse(a)
	}
	return out
}
