# Build stage
FROM golang:1.26.5@sha256:079e59808d2d252516e27e3f3a9c003740dee7f75e55aa71528766d52bcfc16a AS builder

WORKDIR /go/src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go install github.com/bufbuild/buf/cmd/buf@v1.65.0 && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.28.0 && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.28.0 && \
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
RUN make buf

RUN apt update && apt install zip unzip -y
RUN make update-swagger

ARG VERSION=dev
ARG COMMIT=""
ARG DATE=""

ENV CGO_ENABLED=0
RUN go build -cover \
    -ldflags="-s -w -X github.com/cvewatcher/mulval/cmd/mulval.Version=${VERSION} -X github.com/cvewatcher/mulval/cmd/mulval.Commit=${COMMIT} -X github.com/cvewatcher/mulval/cmd/mulval.Date=${DATE} -X github.com/cvewatcher/mulval/cmd/mulval.BuiltBy=docker" \
    -o /go/bin/mulval cmd/mulval/main.go



# Prod stage
FROM pandatix/mulval:v0.1.1@sha256:080c2e0e7d598fa700bbaae71236c905239e895cdd356af79630651922548f7f
COPY --from=builder /go/bin/mulval /mulval
COPY ./gen ./gen
ENTRYPOINT [ "/mulval" ]
