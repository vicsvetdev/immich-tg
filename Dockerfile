# syntax=docker/dockerfile:1

FROM golang:1.27 AS build
WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/immich-tg ./cmd/immich-tg

# Static base with CA certificates and time zone data, running as non-root.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/immich-tg /immich-tg
USER nonroot:nonroot
ENTRYPOINT ["/immich-tg"]
