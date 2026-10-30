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
    go build -trimpath -ldflags "-X github.com/rusher2004/screeny-bird/commands.version=${VERSION}" -o /out/screeny ./cmd

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/screeny /screeny

USER nonroot:nonroot
ENTRYPOINT ["/screeny"]
