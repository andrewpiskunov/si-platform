FROM golang:1.27.3-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -o /out/demo ./cmd/demo \
 && CGO_ENABLED=0 go build -trimpath -o /out/admin ./cmd/admin \
 && CGO_ENABLED=0 go build -trimpath -o /out/catalogue-import ./cmd/catalogue-import \
 && CGO_ENABLED=0 go build -trimpath -o /out/sibrowser-sync ./cmd/sibrowser-sync \
 && mkdir -p /runtime-data /runtime-tmp

FROM debian:trixie-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates ffmpeg bubblewrap util-linux \
 && for lib in /usr/lib/*-linux-gnu/pulseaudio/*.so; do ln -s "$lib" "$(dirname "$(dirname "$lib")")/$(basename "$lib")"; done \
 && apt-get clean \
 && find /var/lib/apt/lists -mindepth 1 -delete
COPY --from=build /out/ /app/
COPY --from=build --chown=65532:65532 /runtime-data/ /data/
COPY --from=build --chown=65532:65532 /runtime-tmp/ /tmp/
ENV DATA_DIR=/data
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/server"]
