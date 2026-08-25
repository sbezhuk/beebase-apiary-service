package apiary

import (
	"time"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
)

// Response is the public representation of an apiary.
type Response struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Location  string    `json:"location"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func newResponse(a *apiary.Apiary) Response {
	return Response{
		ID:        a.ID,
		Name:      a.Name,
		Location:  a.Location,
		Notes:     a.Notes,
		CreatedAt: a.CreatedAt,
		UpdatedAt: a.UpdatedAt,
	}
}

func newListResponse(apiaries []*apiary.Apiary) []Response {
	out := make([]Response, len(apiaries))
	for i, a := range apiaries {
		out[i] = newResponse(a)
	}
	return out
}
