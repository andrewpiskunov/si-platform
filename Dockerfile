FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -o /out/demo ./cmd/demo \
 && CGO_ENABLED=0 go build -trimpath -o /out/admin ./cmd/admin \
 && mkdir -p /runtime-data /runtime-tmp

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/ /app/
COPY --from=build --chown=65532:65532 /runtime-data/ /data/
COPY --from=build --chown=65532:65532 /runtime-tmp/ /tmp/
ENV DATA_DIR=/data
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/server"]
