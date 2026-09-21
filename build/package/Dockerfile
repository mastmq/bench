# syntax=docker/dockerfile:1

FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0
RUN go build -trimpath -o /out/mastbench ./cmd/mastbench

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mastbench /usr/local/bin/mastbench
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mastbench"]
