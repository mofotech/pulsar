# ── Stage 1: build ────────────────────────────────────────────────────────────
# BUILDPLATFORM = the machine doing the build (e.g. linux/arm64 on Apple Silicon).
# The runtime stage is pinned to linux/amd64 so the image always runs on x86_64
# servers regardless of where it is built.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-s -w \
      -X github.com/agomez/pulsar/internal/version.Version=${VERSION} \
      -X github.com/agomez/pulsar/internal/version.GitCommit=${GIT_COMMIT} \
      -X github.com/agomez/pulsar/internal/version.BuildDate=${BUILD_DATE}" \
    -o /pulsar ./cmd/pulsar

# ── Stage 2: runtime ──────────────────────────────────────────────────────────
# Pinned to linux/amd64 — servers are x86_64. Building on Apple Silicon without
# this explicit platform causes apt to install arm64 binaries into the image,
# which then fail with "exec format error" on amd64 hosts.
FROM --platform=linux/amd64 debian:12-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl \
      # compute agent: disk image tools + cloud-init ISO
      qemu-utils genisoimage \
      # compute agent: virsh for hot-attach/detach disks
      libvirt-clients \
      # network agent: OVN/OVS management
      ovn-host \
      # storage agent: LVM thin provisioning + iSCSI target
      lvm2 thin-provisioning-tools tgt \
      # compute agent: iSCSI initiator for volume attach
      open-iscsi \
      # compute agent: nsenter to run iscsiadm in host network namespace
      util-linux \
 && rm -rf /var/lib/apt/lists/*

COPY --from=builder /pulsar /pulsar

EXPOSE 8080 9090 9091

ENTRYPOINT ["/pulsar"]
