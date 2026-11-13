# Pinned by digest, not just tag -- re-resolve with:
#   docker pull golang:1.27.4-alpine && docker inspect --format '{{index .RepoDigests 0}}' golang:1.27.4-alpine
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=builder /out/api /api
USER nonroot:nonroot
EXPOSE __PORT__
ENTRYPOINT ["/api"]
