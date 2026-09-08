# Builds the MCP server for directories that run servers in a container to
# inspect them (Glama and the like). VirtualBox is not in the image, so every
# tool answers with doctor's report instead of a machine; tool discovery and
# the handshake work as on a real host.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The version comes from npm/package.json, the one place a release bumps by hand.
RUN VERSION=$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' npm/package.json) \
 && CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/chryaner/terrarium/internal/core.Version=$VERSION" \
      -o /out/terrarium ./cmd/terrarium

FROM alpine:3.20
COPY --from=build /out/terrarium /usr/local/bin/terrarium
# terrarium keeps its state under LOCALAPPDATA on Windows; give it a home here.
ENV LOCALAPPDATA=/data
RUN mkdir -p /data
ENTRYPOINT ["/usr/local/bin/terrarium", "mcp"]
