package apiary

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func floatPtr(f float64) *float64 { return &f }

func TestCreateRequest_Validate(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRequest
		want map[string]string
	}{
		{
			name: "valid",
			req:  CreateRequest{Name: "Home apiary", Location: "Backyard", Description: "n/a"},
			want: map[string]string{},
		},
		{
			name: "empty name",
			req:  CreateRequest{Name: "", Location: "Backyard"},
			want: map[string]string{"name": CodeNameRequired},
		},
		{
			name: "whitespace-only name",
			req:  CreateRequest{Name: "   "},
			want: map[string]string{"name": CodeNameRequired},
		},
		{
			name: "name too long",
			req:  CreateRequest{Name: strings.Repeat("a", maxNameLength+1)},
			want: map[string]string{"name": CodeNameTooLong},
		},
		{
			name: "location too long",
			req:  CreateRequest{Name: "ok", Location: strings.Repeat("a", maxLocationLength+1)},
			want: map[string]string{"location": CodeLocationTooLong},
		},
		{
			name: "description too long",
			req:  CreateRequest{Name: "ok", Description: strings.Repeat("a", maxDescriptionLength+1)},
			want: map[string]string{"description": CodeDescriptionTooLong},
		},
		{
			name: "location and description optional",
			req:  CreateRequest{Name: "ok"},
			want: map[string]string{},
		},
		{
			name: "lat and lon optional",
			req:  CreateRequest{Name: "ok"},
			want: map[string]string{},
		},
		{
			name: "valid coordinates",
			req:  CreateRequest{Name: "ok", Lat: floatPtr(45.5), Lon: floatPtr(-122.6)},
			want: map[string]string{},
		},
		{
			name: "lat out of range",
			req:  CreateRequest{Name: "ok", Lat: floatPtr(90.1)},
			want: map[string]string{"lat": CodeLatOutOfRange},
		},
		{
			name: "lat below range",
			req:  CreateRequest{Name: "ok", Lat: floatPtr(-90.1)},
			want: map[string]string{"lat": CodeLatOutOfRange},
		},
		{
			name: "lon out of range",
			req:  CreateRequest{Name: "ok", Lon: floatPtr(180.1)},
			want: map[string]string{"lon": CodeLonOutOfRange},
		},
		{
			name: "lon below range",
			req:  CreateRequest{Name: "ok", Lon: floatPtr(-180.1)},
			want: map[string]string{"lon": CodeLonOutOfRange},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.req.Validate()
			if len(got) != len(tt.want) {
				t.Fatalf("Validate() = %v, want %v", got, tt.want)
			}
			for field, wantCode := range tt.want {
				if gotCode, ok := got[field]; !ok || gotCode != wantCode {
					t.Errorf("field %q: got code %q, want %q", field, gotCode, wantCode)
				}
			}
		})
	}
}

func TestCreateRequest_Validate_Images(t *testing.T) {
	if fields := (&CreateRequest{Name: "ok", Images: nil}).Validate(); len(fields) != 0 {
		t.Errorf("nil images: expected no errors, got %v", fields)
	}
	if fields := (&CreateRequest{Name: "ok", Images: []string{}}).Validate(); len(fields) != 0 {
		t.Errorf("empty images: expected no errors, got %v", fields)
	}
	if fields := (&CreateRequest{Name: "ok", Images: []string{uuid.New().String()}}).Validate(); len(fields) != 0 {
		t.Errorf("valid image id: expected no errors, got %v", fields)
	}

	fields := (&CreateRequest{Name: "ok", Images: []string{"not-a-uuid"}}).Validate()
	if code := fields["images"]; code != CodeImagesInvalid {
		t.Errorf("images code = %q, want %q", code, CodeImagesInvalid)
	}
}

func TestUpdateRequest_Validate(t *testing.T) {
	if fields := (&UpdateRequest{Name: "ok"}).Validate(); len(fields) != 0 {
		t.Errorf("expected no errors, got %v", fields)
	}

	fields := (&UpdateRequest{Name: ""}).Validate()
	if code := fields["name"]; code != CodeNameRequired {
		t.Errorf("name code = %q, want %q", code, CodeNameRequired)
	}
}

func TestUpdateRequest_Validate_Images(t *testing.T) {
	if fields := (&UpdateRequest{Name: "ok", Images: nil}).Validate(); len(fields) != 0 {
		t.Errorf("nil images: expected no errors, got %v", fields)
	}
	if fields := (&UpdateRequest{Name: "ok", Images: []string{}}).Validate(); len(fields) != 0 {
		t.Errorf("empty images: expected no errors, got %v", fields)
	}
	if fields := (&UpdateRequest{Name: "ok", Images: []string{uuid.New().String()}}).Validate(); len(fields) != 0 {
		t.Errorf("valid image id: expected no errors, got %v", fields)
	}

	fields := (&UpdateRequest{Name: "ok", Images: []string{"not-a-uuid"}}).Validate()
	if code := fields["images"]; code != CodeImagesInvalid {
		t.Errorf("images code = %q, want %q", code, CodeImagesInvalid)
	}
}
