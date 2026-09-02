package apiary

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/sbezhuk/beebase-common/httpx"
)

const (
	maxNameLength        = 200
	maxLocationLength    = 500
	maxDescriptionLength = 2000

	minLat = -90
	maxLat = 90
	minLon = -180
	maxLon = 180
)

// Field validation error codes. Each is a stable key a client can map to a
// localized message; the field carrying no error is simply absent from the
// response's "fields" map.
const (
	CodeNameRequired       = "name_required"
	CodeNameTooLong        = "name_too_long"
	CodeLocationTooLong    = "location_too_long"
	CodeDescriptionTooLong = "description_too_long"
	CodeLatOutOfRange      = "lat_out_of_range"
	CodeLonOutOfRange      = "lon_out_of_range"
	CodeImagesInvalid      = "images_invalid"
)

// validatable is implemented by every request DTO in this package.
// Validate returns a map of field name to error code, empty if valid.
type validatable interface {
	Validate() map[string]string
}

// decodeAndValidate decodes the request body into dst and validates it,
// writing an appropriate error response and returning false if either step
// fails.
func decodeAndValidate(w http.ResponseWriter, r *http.Request, dst validatable) bool {
	defer func() { _ = r.Body.Close() }()

	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeInvalidBody, "request body must be valid JSON")
		return false
	}

	if fields := dst.Validate(); len(fields) > 0 {
		httpx.WriteValidationError(w, fields)
		return false
	}

	return true
}

// CreateRequest is the body of POST /apiaries.
type CreateRequest struct {
	Name        string   `json:"name"`
	Location    string   `json:"location"`
	Description string   `json:"description"`
	Lat         *float64 `json:"lat"`
	Lon         *float64 `json:"lon"`
	// Images is the set of already-uploaded media ids to attach
	// immediately - unlike UpdateRequest.Images, there's no "leave alone"
	// case here since there's nothing to leave alone yet, so an absent/
	// empty images just means no photos.
	Images []string `json:"images"`
}

func (r *CreateRequest) Validate() map[string]string {
	fields := validateFields(r.Name, r.Location, r.Description, r.Lat, r.Lon)
	validateImages(r.Images, fields)
	return fields
}

// UpdateRequest is the body of PUT /apiaries/{apiaryID}. Update replaces
// all fields (PUT semantics), not a partial patch, so the same rules apply
// as on create.
type UpdateRequest struct {
	Name        string   `json:"name"`
	Location    string   `json:"location"`
	Description string   `json:"description"`
	Lat         *float64 `json:"lat"`
	Lon         *float64 `json:"lon"`
	// Images, when present (even as an empty array), is the desired
	// final set of already-uploaded media IDs attached to this apiary;
	// omitting the field (or sending JSON null) leaves currently
	// attached media untouched. Go's json package already distinguishes
	// "absent/null" (nil slice) from "[]" (non-nil, empty slice), which
	// is exactly the distinction this needs.
	Images []string `json:"images"`
}

func (r *UpdateRequest) Validate() map[string]string {
	fields := validateFields(r.Name, r.Location, r.Description, r.Lat, r.Lon)
	validateImages(r.Images, fields)
	return fields
}

// validateImages checks that every id in images is a well-formed UUID,
// setting fields["images"] on the first failure found.
func validateImages(images []string, fields map[string]string) {
	for _, id := range images {
		if _, err := uuid.Parse(id); err != nil {
			fields["images"] = CodeImagesInvalid
			return
		}
	}
}

func validateFields(name, location, description string, lat, lon *float64) map[string]string {
	fields := map[string]string{}

	switch {
	case strings.TrimSpace(name) == "":
		fields["name"] = CodeNameRequired
	case len(name) > maxNameLength:
		fields["name"] = CodeNameTooLong
	}

	if len(location) > maxLocationLength {
		fields["location"] = CodeLocationTooLong
	}
	if len(description) > maxDescriptionLength {
		fields["description"] = CodeDescriptionTooLong
	}

	if lat != nil && (*lat < minLat || *lat > maxLat) {
		fields["lat"] = CodeLatOutOfRange
	}
	if lon != nil && (*lon < minLon || *lon > maxLon) {
		fields["lon"] = CodeLonOutOfRange
	}

	return fields
}
