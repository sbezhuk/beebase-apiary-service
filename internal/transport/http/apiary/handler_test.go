package apiary

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	appapiary "github.com/sbezhuk/beebase-apiary-service/internal/application/apiary"
	"github.com/sbezhuk/beebase-apiary-service/internal/domain/apiary"
)

func TestWriteServiceError(t *testing.T) {
	h := NewHandler(nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "")

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "apiary not found",
			err:        apiary.ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantCode:   CodeApiaryNotFound,
		},
		{
			name:       "apiary limit reached",
			err:        appapiary.ErrApiaryLimitReached,
			wantStatus: http.StatusForbidden,
			wantCode:   CodeApiaryLimitReached,
		},
		{
			name:       "apiary name exists",
			err:        apiary.ErrNameTaken,
			wantStatus: http.StatusConflict,
			wantCode:   CodeApiaryNameExists,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.writeServiceError(rec, tt.err)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode json: %v", err)
			}

			if body.Error.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tt.wantCode)
			}
		})
	}
}
