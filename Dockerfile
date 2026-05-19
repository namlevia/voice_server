FROM golang:1.24-bullseye AS builder
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends libc++1 libc++abi1 build-essential ca-certificates && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags="-s -w" -o voice_server main.go
RUN set -eux; \
    mkdir -p /app/runtime-lib; \
    arch="$(go env GOARCH)"; \
    case "$arch" in \
      arm64) sherpa_arch="aarch64-unknown-linux-gnu" ;; \
      amd64) sherpa_arch="x86_64-unknown-linux-gnu" ;; \
      arm) sherpa_arch="arm-unknown-linux-gnueabihf" ;; \
      *) echo "unsupported GOARCH: $arch"; exit 1 ;; \
    esac; \
    mod_dir="$(go env GOPATH)/pkg/mod/github.com/k2-fsa/sherpa-onnx-go-linux@v1.12.4/lib/$sherpa_arch"; \
    cp "$mod_dir"/*.so /app/runtime-lib/

FROM python:3.11-slim-bookworm AS model-downloader
WORKDIR /model
RUN apt-get update && apt-get install -y --no-install-recommends git git-lfs ca-certificates && rm -rf /var/lib/apt/lists/*
RUN git lfs install
RUN git clone --depth 1 https://huggingface.co/hynt/Zipformer-30M-RNNT-6000h /model && git lfs pull

FROM ubuntu:22.04 AS final
WORKDIR /app
RUN apt-get update && apt-get install -y --no-install-recommends libc++1 libc++abi1 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=builder /app/voice_server .
COPY --from=builder /app/runtime-lib ./lib
COPY --from=builder /app/lib/ten-vad/lib/Linux/x64/libten_vad.so ./lib/
COPY --from=builder /app/static ./static
COPY --from=builder /app/config.json ./config.json
COPY --from=model-downloader /model ./models/asr/zipformer-vietnamese-30m
ENV LD_LIBRARY_PATH=/app/lib
EXPOSE 9000
CMD ["./voice_server"]
