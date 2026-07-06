# Image-based deployment. The simpler install is still the bare binary with
# packaging/rookery.service; see packaging/rookery.container for running this
# image as a Quadlet with the required mounts.

FROM --platform=$BUILDPLATFORM docker.io/library/node:22 AS webbuild
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.25 AS build
ARG TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY . .
COPY --from=webbuild /src/web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=linux GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o /rookery ./cmd/rookery

# Not FROM scratch: rookery shells out to systemctl/journalctl (against the
# host systemd via the mounted /run/systemd), git for history, ssh for remote
# hosts. The quadlet generator is bind-mounted from the host instead of
# installed, so validation always matches the Podman that runs the units.
FROM registry.fedoraproject.org/fedora-minimal:42
RUN microdnf -y --setopt=install_weak_deps=0 install systemd git-core openssh-clients \
    && microdnf clean all
COPY --from=build /rookery /usr/local/bin/rookery

# Inside a container, loopback is unreachable from outside; publish the port
# with a loopback-only host mapping instead (see packaging/rookery.container).
ENV ROOKERY_LISTEN=0.0.0.0:7665
EXPOSE 7665
ENTRYPOINT ["/usr/local/bin/rookery"]
