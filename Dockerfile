# syntax=docker/dockerfile:1
#
# Multi-stage build for the single CuriosityEngine service.
# The runtime image is distroless/static: no shell, no package manager, non-root,
# a few MB in size. A small image is also a fast cold start, which matters
# because Discord gives an interaction webhook only 3 seconds to respond.

# ---- build stage ----
FROM golang:1.25 AS build
WORKDIR /src

# Download modules first so this layer is cached across source-only changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# CGO disabled => a fully static binary that runs on the distroless/static base.
ENV CGO_ENABLED=0 GOOS=linux
RUN go build -trimpath -ldflags="-s -w" -o /app/server ./cmd/server

# ---- runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /app/server /app/server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/app/server"]
