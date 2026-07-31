FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/skillet ./cmd/skillet
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/skillet-eval ./cmd/skillet-eval

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates git \
    && rm -rf /var/lib/apt/lists/* \
    && mkdir -p /var/lib/skillet \
    && chown 65532:65532 /var/lib/skillet
COPY --from=build /out/skillet /usr/local/bin/skillet
COPY --from=build /out/skillet-eval /usr/local/bin/skillet-eval
WORKDIR /var/lib/skillet
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/skillet"]
