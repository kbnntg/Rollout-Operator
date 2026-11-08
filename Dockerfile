# syntax=docker/dockerfile:1
# Operator镜像：多阶段构建，最终镜像使用distroless非root用户。
FROM golang:1.24 AS builder
ARG GOPROXY=https://goproxy.cn,direct
ENV CGO_ENABLED=0 \
    GOPROXY=${GOPROXY}
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY api api
COPY cmd cmd
COPY internal internal
RUN go build -trimpath -ldflags="-s -w" -o manager ./cmd/manager

FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager /manager
USER 65532:65532
ENTRYPOINT ["/manager"]