# Build stage
FROM golang:1.27@sha256:c5b07c17f54c5f22230c4b4da6e90249165cf55368e01a52808cb92064e18836 AS builder

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
FROM pandatix/mulval:v0.1.2@sha256:50fd334857b73c73470109f78c0fa57379bd95959534a2bbf397fd37d10e3f41
COPY --from=builder /go/bin/mulval /mulval
COPY ./gen ./gen
ENTRYPOINT [ "/mulval" ]
