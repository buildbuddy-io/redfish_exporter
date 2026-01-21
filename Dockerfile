FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 go build -o /build/redfish_exporter

FROM alpine:3.19

COPY --from=builder /build/redfish_exporter /redfish_exporter
COPY config.example.yml /redfish_exporter.yml
CMD ["/redfish_exporter", "--config.file", "/redfish_exporter.yml"]
EXPOSE 9610
