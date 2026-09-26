FROM golang:1.27.1-alpine AS build
ARG VERSION=dev
ARG REVISION=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w \
        -X github.com/VibhuviOiO/openldap-exporter/internal/collector.Version=${VERSION} \
        -X github.com/VibhuviOiO/openldap-exporter/internal/collector.Revision=${REVISION}" \
      -o /openldap-exporter ./cmd/openldap-exporter

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /openldap-exporter /openldap-exporter
COPY openldap-exporter.example.yml /etc/openldap-exporter/openldap-exporter.yml
USER nonroot:nonroot
EXPOSE 9330
ENTRYPOINT ["/openldap-exporter"]
CMD ["-config", "/etc/openldap-exporter/openldap-exporter.yml"]
