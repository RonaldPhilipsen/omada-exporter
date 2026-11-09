# Build stage: cross-compile a static binary for the target platform.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:fa3d596898d9f126af7c01d14ee7597bab3f39cccf392ef36b69fe77cd681f18 AS build

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
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build /out/omada-exporter /omada-exporter

ENTRYPOINT ["/omada-exporter"]

# OCI image labels. Keep this block last so that changed values only rebuild this step.
ARG VERSION=0.1.0
ARG GIT_SHA=unknown
LABEL \
    org.opencontainers.image.title="omada-exporter" \
    org.opencontainers.image.description="OpenTelemetry metrics exporter for TP-Link Omada controllers" \
    org.opencontainers.image.url="https://github.com/RonaldPhilipsen/omada-exporter" \
    org.opencontainers.image.source="https://github.com/RonaldPhilipsen/omada-exporter" \
    org.opencontainers.image.version="${VERSION}" \
    org.opencontainers.image.revision="${GIT_SHA}"
