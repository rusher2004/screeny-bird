FROM --platform=$BUILDPLATFORM golang:1.27.3 AS build

# The target platform and version are required build arguments: the image has
# no defaults and a build without them fails.
ARG TARGETOS
ARG TARGETARCH
ARG VERSION

RUN test -n "$TARGETOS" && test -n "$TARGETARCH" && test -n "$VERSION"

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -ldflags "-X main.version=${VERSION}" -o /out/screeny ./cmd

# An empty data directory the nonroot user owns, so a volume mounted there is
# writable by the sync command.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/screeny /screeny
COPY --from=build --chown=65532:65532 /out/data /data

USER nonroot:nonroot
ENTRYPOINT ["/screeny"]
