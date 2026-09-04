package apiary

import (
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
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
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// newResponse builds a Response for a. Images is read straight from a -
// never nil (Apiary.Images is always a real, possibly-empty slice) - so
// it renders as "images": [] rather than null when there are no photos.
func newResponse(a *apiary.Apiary, publicBaseURL string) Response {
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
		CreatedAt:   a.CreatedAt,
		UpdatedAt:   a.UpdatedAt,
	}
}

func newListResponse(apiaries []*apiary.Apiary, publicBaseURL string) []Response {
	out := make([]Response, len(apiaries))
	for i, a := range apiaries {
		out[i] = newResponse(a, publicBaseURL)
	}
	return out
}
