// Upload answers 202: OCR runs behind the gateway and results arrive via
// Kafka events.
package document

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	"github.com/disillusioned-labs/expense/internal/handler"
	documentservice "github.com/disillusioned-labs/expense/internal/service/document"
)

// maxUploadBytes caps a single nota upload, matching the OCR worker cap.
const maxUploadBytes = 25 << 20

var tracer = otel.Tracer("handler/document")

type Handler struct {
	service documentservice.DocumentService
	log     *slog.Logger
}

func NewDocumentHandler(service documentservice.DocumentService, log *slog.Logger) *Handler {
	return &Handler{service: service, log: log}
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

	in, ok := h.decodeUpload(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid upload")
		return
	}

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

	in, ok := h.decodeUpload(w, r)
	if !ok {
		span.SetStatus(codes.Error, "invalid upload")
		return
	}

	doc, err := h.service.AttachToTransaction(ctx, actor, transactionID, in)
	if err != nil {
		handler.WriteServiceError(w, r, h.log, err)
		return
	}
	handler.OK(w, http.StatusAccepted, toDocumentResponse(doc))
}

func (h *Handler) decodeUpload(w http.ResponseWriter, r *http.Request) (documentservice.UploadInput, bool) {
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "invalid multipart form")
		return documentservice.UploadInput{}, false
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		handler.WriteError(w, http.StatusBadRequest, handler.CodeBadRequest, "missing file field")
		return documentservice.UploadInput{}, false
	}
	defer func() { _ = file.Close() }()

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return documentservice.UploadInput{
		FileName: header.Filename, MimeType: mimeType, Size: header.Size, Content: file,
	}, true
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
