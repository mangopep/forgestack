# Pinned by digest, not just tag (F4 supply-chain hardening): a mutable tag
# can be repointed; a digest cannot. Dependabot's docker ecosystem (already
# configured) opens a PR when either image updates — re-resolve the digest
# locally (`docker pull <image> && docker inspect --format
# '{{index .RepoDigests 0}}' <image>`) and verify the build/tests/scans
# still pass before merging, rather than trusting the new digest blindly.
FROM golang:1.27.4-alpine@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS builder
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/api ./cmd/api
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=builder /out/api /api
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/api"]
