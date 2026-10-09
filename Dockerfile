# towline-mcp image, used to run the remote MCP gateway:
#   docker run ... ghcr.io/changethisusername/towline-mcp gateway
ARG GO_IMAGE=golang:1.25
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
    -o /out/towline-mcp ./cmd/towline-mcp \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/towline-mcp /towline-mcp
# A fresh named volume mounted here inherits this ownership, so the
# non-root user can write its state.
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/towline-mcp", "gateway", "healthcheck"]
ENTRYPOINT ["/towline-mcp"]
CMD ["gateway"]
