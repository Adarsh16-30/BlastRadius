# Builder tag verified with `docker manifest inspect golang:1.27.4-alpine3.24` (2026-11-15).
FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# scratch: static binary only, no shell. Non-root numeric user.
FROM scratch
COPY --from=build /out/api /api
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/api"]
