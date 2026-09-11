# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/disillusioned-labs/expense/internal/app.version=${VERSION} \
        -X github.com/disillusioned-labs/expense/internal/app.commit=${COMMIT} \
        -X github.com/disillusioned-labs/expense/internal/app.buildDate=${BUILD_DATE}" \
      -o /out/api ./cmd/api
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/disillusioned-labs/expense/internal/app.version=${VERSION} \
        -X github.com/disillusioned-labs/expense/internal/app.commit=${COMMIT} \
        -X github.com/disillusioned-labs/expense/internal/app.buildDate=${BUILD_DATE}" \
      -o /out/worker ./cmd/worker
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w \
        -X github.com/disillusioned-labs/expense/internal/app.version=${VERSION} \
        -X github.com/disillusioned-labs/expense/internal/app.commit=${COMMIT} \
        -X github.com/disillusioned-labs/expense/internal/app.buildDate=${BUILD_DATE}" \
      -o /out/consumer ./cmd/consumer


# ---------------------------------------------------------------------------
# Runtime image
# ---------------------------------------------------------------------------

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app

COPY --from=build /out/api ./api
COPY --from=build /out/worker ./worker
COPY --from=build /out/consumer ./consumer

EXPOSE 8081

# One image, three roles: the entry point is the API; the outbox publisher and
# the OCR consumer run by overriding the command (docker run <img> /app/worker).
ENTRYPOINT ["/app/api"]
