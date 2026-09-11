package document

import (
	"io"
	"time"

	"github.com/google/uuid"
)

type UploadInput struct {
	FileName string
	MimeType string
	Size     int64
	Content  io.Reader
}

type Document struct {
	ID uuid.UUID `json:"id"`
	// TransactionID is zero for a project-level upload: the draft does not
	// exist until the OCR consumer creates it.
	TransactionID uuid.UUID `json:"transaction_id,omitempty"`
	FileName      string    `json:"file_name"`
	FileURL       string    `json:"file_url"`
	MimeType      string    `json:"mime_type"`
	FileSize      int64     `json:"file_size"`
	OcrStatus     string    `json:"ocr_status"`
	CreatedAt     time.Time `json:"created_at"`
}
