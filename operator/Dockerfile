# pipen-operator image: multi-stage build of the Go operator binary onto a
# minimal distroless base. Cluster-generic — all cluster shape comes from
# the pipen-operator-config ConfigMap at runtime.

# hadolint ignore=DL3007
FROM golang:1.26 AS build
WORKDIR /src

# hadolint ignore=DL3018
COPY go.mod go.sum ./
RUN go mod download
COPY api/ api/
COPY internal/ internal/
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/pipen-operator .

# hadolint ignore=DL3007
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/pipen-operator /usr/local/bin/pipen-operator
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/pipen-operator"]
