package apiary

import (
	"strings"
	"testing"
)

func TestCreateRequest_Validate(t *testing.T) {
	tests := []struct {
		name string
		req  CreateRequest
		want map[string]string
	}{
		{
			name: "valid",
			req:  CreateRequest{Name: "Home apiary", Location: "Backyard", Notes: "n/a"},
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
			name: "notes too long",
			req:  CreateRequest{Name: "ok", Notes: strings.Repeat("a", maxNotesLength+1)},
			want: map[string]string{"notes": CodeNotesTooLong},
		},
		{
			name: "location and notes optional",
			req:  CreateRequest{Name: "ok"},
			want: map[string]string{},
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

func TestUpdateRequest_Validate(t *testing.T) {
	if fields := (&UpdateRequest{Name: "ok"}).Validate(); len(fields) != 0 {
		t.Errorf("expected no errors, got %v", fields)
	}

	fields := (&UpdateRequest{Name: ""}).Validate()
	if code := fields["name"]; code != CodeNameRequired {
		t.Errorf("name code = %q, want %q", code, CodeNameRequired)
	}
}
