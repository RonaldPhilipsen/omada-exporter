# Build stage: cross-compile a static binary for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

ARG TARGETARCH
ARG VERSION=0.1.0

ENV GOARCH=${TARGETARCH}
ENV CGO_ENABLED=0

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/omada-exporter ./cmd/omada-exporter

# Runtime stage: non-root distroless image with the single binary.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/omada-exporter /omada-exporter

ENTRYPOINT ["/omada-exporter"]
