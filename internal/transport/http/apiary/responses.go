package apiary

import (
	"time"

	"github.com/google/uuid"

	appapiary "github.com/sbezhuk/beebase-apiary-service/internal/application/apiary"
	"github.com/sbezhuk/beebase-common/medialink"
)

// ImageResponse is the public representation of one image attached to an
// apiary: its media id, plus the URL a client loads/caches it from. The
// URL is derived, not stored - it's always media-service's stable
// download route, built fresh on every response.
type ImageResponse struct {
	ID       uuid.UUID `json:"id"`
	ImageURL string    `json:"image_url"`
}

// Response is the public representation of an apiary.
type Response struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Location    string          `json:"location"`
	Description string          `json:"description"`
	Lat         *float64        `json:"lat"`
	Lon         *float64        `json:"lon"`
	Images      []ImageResponse `json:"images"`
	// Writable reports whether the caller can currently edit this apiary,
	// create hives under it, or otherwise mutate it or its descendants.
	// Always true under Pro; under Free, true only for the one apiary
	// within the caller's entitlement (see application/apiary.
	// Service.isWritable). Lets Flutter render locked-resource UI without
	// reimplementing this selection itself.
	Writable  bool      `json:"writable"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// newResponse builds a Response for a. Images is read straight from a -
// never nil (Apiary.Images is always a real, possibly-empty slice) - so
// it renders as "images": [] rather than null when there are no photos.
func newResponse(a *appapiary.WithAccess, publicBaseURL string) Response {
	images := make([]ImageResponse, len(a.Images))
	for i, id := range a.Images {
		images[i] = ImageResponse{ID: id, ImageURL: medialink.DownloadURL(publicBaseURL, id)}
	}
	return Response{
		ID:          a.ID,
		Name:        a.Name,
		Location:    a.Location,
		Description: a.Description,
		Lat:         a.Lat,
		Lon:         a.Lon,
		Images:      images,
		Writable:    a.Writable,
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

func newListResponse(apiaries []*appapiary.WithAccess, publicBaseURL string) []Response {
	out := make([]Response, len(apiaries))
	for i, a := range apiaries {
		out[i] = newResponse(a, publicBaseURL)
	}
	return out
}

// WritableApiaryResponse is the public representation of GET
// /apiaries/writable - hive-service's sole authoritative source for
// "which of this user's apiaries currently falls within their Free
// entitlement", used to resolve a specific hive's parent-apiary
// writability without duplicating apiary-service's selection algorithm.
type WritableApiaryResponse struct {
	// Unrestricted is true when the caller currently has Pro: every
	// apiary they own is writable, and ApiaryID is always null.
	Unrestricted bool `json:"unrestricted"`
	// ApiaryID is the id of the one apiary within the caller's Free
	// entitlement, or null if they own none. Only meaningful when
	// Unrestricted is false.
	ApiaryID *uuid.UUID `json:"apiary_id"`
}
