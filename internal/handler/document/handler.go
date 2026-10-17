// Upload answers 202: OCR runs behind the gateway and results arrive via
// Kafka events.
package document

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/handler"
	documentservice "github.com/disillusioned-labs/expense/internal/service/document"
)

// maxUploadBytes caps a single nota upload, matching the OCR worker cap.
const (
	maxUploadBytes        = 25 << 20
	maxUploadMemoryBytes  = 1 << 20
	maxMultipartExtraSize = 1 << 20
)

var tracer = otel.Tracer("handler/document")

type Handler struct {
	service documentservice.DocumentService
	log     *slog.Logger
}

func NewDocumentHandler(service documentservice.DocumentService, log *slog.Logger) *Handler {
	return &Handler{service: service, log: log}
}

func uploadIdempotencyKey(r *http.Request) (uuid.UUID, bool) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return uuid.Nil, false
	}
	key, err := uuid.Parse(strings.TrimSpace(values[0]))
	return key, err == nil && key != uuid.Nil
}

func (h *Handler) documentID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid document id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) projectID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid project id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) transactionID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid transaction id")
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) uploadToProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.uploadToProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	projectID, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}
	idempotencyKey, ok := uploadIdempotencyKey(r)
	if !ok {
		handler.WriteError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "a non-empty UUID Idempotency-Key header is required")
		span.SetStatus(codes.Error, "invalid idempotency key")
		return
	}

	in, cleanupUpload, ok := h.decodeUpload(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid upload")
		return
	}
	defer cleanupUpload()
	in.IdempotencyKey = idempotencyKey

	doc, err := h.service.UploadToProject(ctx, actor, projectID, in)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusAccepted, toDocumentResponse(doc))
}

func (h *Handler) attachToTransaction(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.attachToTransaction")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	transactionID, ok := h.transactionID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid transaction id")
		return
	}
	idempotencyKey, ok := uploadIdempotencyKey(r)
	if !ok {
		handler.WriteError(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "a non-empty UUID Idempotency-Key header is required")
		span.SetStatus(codes.Error, "invalid idempotency key")
		return
	}

	in, cleanupUpload, ok := h.decodeUpload(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid upload")
		return
	}
	defer cleanupUpload()
	in.IdempotencyKey = idempotencyKey

	doc, err := h.service.AttachToTransaction(ctx, actor, transactionID, in)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusAccepted, toDocumentResponse(doc))
}

func (h *Handler) decodeUpload(w http.ResponseWriter, r *http.Request) (documentservice.UploadInput, func(), bool) {
	cleanupForm := func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+maxMultipartExtraSize)
	if err := r.ParseMultipartForm(maxUploadMemoryBytes); err != nil {
		cleanupForm()
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			handler.WriteError(w, http.StatusRequestEntityTooLarge, handler.CodePayloadTooLarge, "upload exceeds the maximum request size")
		} else {
			handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid multipart form")
		}
		return documentservice.UploadInput{}, nil, false
	}
	fileCount := 0
	if r.MultipartForm != nil {
		for _, files := range r.MultipartForm.File {
			fileCount += len(files)
		}
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["file"]) != 1 || fileCount != 1 {
		cleanupForm()
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "exactly one file is required")
		return documentservice.UploadInput{}, nil, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		cleanupForm()
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid file field")
		return documentservice.UploadInput{}, nil, false
	}
	cleanup := func() {
		_ = file.Close()
		cleanupForm()
	}
	if header.Size == 0 {
		cleanup()
		handler.WriteError(w, http.StatusUnprocessableEntity, "FILE_EMPTY", "file is empty")
		return documentservice.UploadInput{}, nil, false
	}
	if header.Size > maxUploadBytes {
		cleanup()
		handler.WriteError(w, http.StatusRequestEntityTooLarge, handler.CodePayloadTooLarge, "file exceeds the 25 MiB limit")
		return documentservice.UploadInput{}, nil, false
	}
	prefix := make([]byte, 512)
	n, err := io.ReadFull(file, prefix)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		cleanup()
		handler.WriteError(w, http.StatusInternalServerError, handler.CodeInternal, "could not read uploaded file")
		return documentservice.UploadInput{}, nil, false
	}
	mimeType := http.DetectContentType(prefix[:n])
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp", "application/pdf":
	default:
		cleanup()
		handler.WriteError(w, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "choose a JPEG, PNG, WebP, or PDF file")
		return documentservice.UploadInput{}, nil, false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		cleanup()
		handler.WriteError(w, http.StatusInternalServerError, handler.CodeInternal, "could not read uploaded file")
		return documentservice.UploadInput{}, nil, false
	}
	return documentservice.UploadInput{
		FileName: header.Filename, MimeType: mimeType, Size: header.Size, Content: file,
	}, cleanup, true
}

func (h *Handler) listByProject(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listByProject")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	projectID, ok := h.projectID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid project id")
		return
	}

	docs, err := h.service.ListByProject(ctx, actor, projectID)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toDocumentResponses(docs))
}

func (h *Handler) listByTransaction(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.listByTransaction")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	transactionID, ok := h.transactionID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid transaction id")
		return
	}

	docs, err := h.service.ListByTransaction(ctx, actor, transactionID)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toDocumentResponses(docs))
}

func (h *Handler) getDocument(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.getDocument")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.documentID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid document id")
		return
	}

	doc, err := h.service.Get(ctx, actor, id)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, toDocumentResponse(doc))
}

func (h *Handler) deleteDocument(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "Handler.deleteDocument")
	defer span.End()
	r = r.WithContext(ctx)

	actor, ok := handler.ActorFrom(w, r)
	if !ok {
		span.SetStatus(codes.Error, "unauthorized")
		return
	}

	id, ok := h.documentID(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid document id")
		return
	}

	if err := h.service.Delete(ctx, actor, id); err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusOK, DeleteResponse{Deleted: true})
}
