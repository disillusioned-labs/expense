package document

import (
	"time"

	"github.com/google/uuid"

	documentservice "github.com/disillusioned-labs/expense/internal/service/document"
)

type DocumentResponse struct {
	ID        uuid.UUID `json:"id"`
	FileName  string    `json:"file_name"`
	FileURL   string    `json:"file_url"`
	MimeType  string    `json:"mime_type"`
	FileSize  int64     `json:"file_size"`
	OcrStatus string    `json:"ocr_status"`
	CreatedAt time.Time `json:"created_at"`
}

func toDocumentResponse(d documentservice.Document) DocumentResponse {
	return DocumentResponse{
		ID:        d.ID,
		FileName:  d.FileName,
		FileURL:   d.FileURL,
		MimeType:  d.MimeType,
		FileSize:  d.FileSize,
		OcrStatus: d.OcrStatus,
		CreatedAt: d.CreatedAt,
	}
}

func toDocumentResponses(ds []documentservice.Document) []DocumentResponse {
	out := make([]DocumentResponse, 0, len(ds))
	for _, d := range ds {
		out = append(out, toDocumentResponse(d))
	}
	return out
}

type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}
