package apiary

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/sbezhuk/beebase-common/httpx"
)

const (
	maxNameLength     = 200
	maxLocationLength = 500
	maxNotesLength    = 2000
)

// Field validation error codes. Each is a stable key a client can map to a
// localized message; the field carrying no error is simply absent from the
// response's "fields" map.
const (
	CodeNameRequired    = "name_required"
	CodeNameTooLong     = "name_too_long"
	CodeLocationTooLong = "location_too_long"
	CodeNotesTooLong    = "notes_too_long"
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
	Name     string `json:"name"`
	Location string `json:"location"`
	Notes    string `json:"notes"`
}

func (r *CreateRequest) Validate() map[string]string {
	return validateFields(r.Name, r.Location, r.Notes)
}

// UpdateRequest is the body of PUT /apiaries/{apiaryID}. Update replaces
// all three fields (PUT semantics), not a partial patch, so the same
// rules apply as on create.
type UpdateRequest struct {
	Name     string `json:"name"`
	Location string `json:"location"`
	Notes    string `json:"notes"`
}

func (r *UpdateRequest) Validate() map[string]string {
	return validateFields(r.Name, r.Location, r.Notes)
}

func validateFields(name, location, notes string) map[string]string {
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
	if len(notes) > maxNotesLength {
		fields["notes"] = CodeNotesTooLong
	}

	return fields
}
