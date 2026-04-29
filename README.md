# Pulsar

Pulsar is a lightweight IaaS compute orchestration platform with compute, network, and storage pillars. It distributes as a **single Go binary** that runs as either a controller or a data-plane agent.

---

## Architecture

```
                    ┌─────────────┐
   pulsarctl ──────▶│  Controller │ :8080 REST  :9090 gRPC  :9091 Metrics
                    └──────┬──────┘
                           │ gRPC bidirectional stream
            ┌──────────────┼──────────────┐
            ▼              ▼              ▼
      agent-compute  agent-network  agent-storage
      (KVM/libvirt)  (bridges/VXLAN)  (LVM)
```

| Component | Role |
|---|---|
| **Controller** | Horizontally-scalable REST + gRPC gateway. Stores state in etcd, audit/quota ledger in PostgreSQL. |
| **agent-compute** | Runs on KVM hosts; manages instances via libvirt. |
| **agent-network** | Runs with host networking; manages bridges, VXLANs, iptables. |
| **agent-storage** | Runs privileged; manages LVM thin-pool volumes. |

---

## Prerequisites

| Tool | Version | Notes |
|---|---|---|
| Go | ≥ 1.24 | `go install` or <https://go.dev/dl/> |
| Docker + Compose | any recent | for the full stack |
| protoc + plugins | ≥ 27 | only needed to regenerate proto stubs |
| golangci-lint | any | optional, for `make lint` |

For the **test host** (Ubuntu 24.04 + KVM):

```
/dev/kvm                           # nested virt or bare-metal
/var/run/libvirt/libvirt-sock      # libvirtd must be running
/var/lib/pulsar/images             # created automatically
/var/lib/pulsar/instances          # created automatically
```

---

## Building

```bash
# Server binary  (bin/pulsar)
make build

# CLI client     (bin/pulsarctl)
make pulsarctl

# Both via Go directly
go build -o bin/pulsar    ./cmd/pulsar
go build -o bin/pulsarctl ./cmd/pulsarctl

# Docker image   (pulsar:dev)
make docker

# Regenerate protobuf stubs (requires protoc)
make proto
```

---

## Local development

`make dev-up` starts only etcd and Postgres in Docker, leaving the controller and agents to run on your host:

```bash
make dev-up                              # etcd :2379  postgres :5432

bin/pulsar controller --config pulsar.yaml

bin/pulsar agent --pillar=compute  --config pulsar.yaml
bin/pulsar agent --pillar=network  --config pulsar.yaml
bin/pulsar agent --pillar=storage  --config pulsar.yaml

make dev-down                            # tear down infra containers
```

Minimal `pulsar.yaml` for local dev:

```yaml
controller:
  listen_http: ":8080"
  listen_grpc: ":9090"
  etcd:
    endpoints: ["localhost:2379"]
  postgres:
    dsn: "postgres://pulsar:pulsar@localhost/pulsar?sslmode=disable"
  jwt:
    secret: "change-me"
    expiry: "24h"

agent:
  controller_grpc: "localhost:9090"
  compute:
    hypervisor_drivers: [kvm]
    image_cache_dir: "/var/lib/pulsar/images"
    instance_dir:    "/var/lib/pulsar/instances"
  network:
    external_interface: "eth0"
    overlay_interface:  "eth0"
    vxlan_port: 4789
  storage:
    backends:
      lvm:
        volume_group: "pulsar-vg"
        thin_pool:    "pulsar-pool"

logging:
  level: "info"
  format: "console"
```

Every config key can be overridden with a `PULSAR_` environment variable (dots become underscores):

```bash
PULSAR_CONTROLLER_JWT_SECRET=my-secret bin/pulsar controller
PULSAR_AGENT_CONTROLLER_GRPC=10.0.0.1:9090 bin/pulsar agent --pillar=compute
```

---

## Testing

```bash
# All unit tests, with race detector
make test

# Sync source to test host and run tests there
make test-remote

# Run linter
make lint
```

Test coverage includes:

| Package | Tests |
|---|---|
| `pkg/fsm` | FSM allowed/denied transitions, error state, concurrent safety |
| `pkg/retry` | First-attempt success, retry-then-succeed, exhausted, context cancel |
| `internal/api` | JWT middleware: missing header, bad format, expired, valid, wrong secret |
| `internal/controller/compute` | Scheduler: least-loaded, hypervisor filtering, no-match |

---

## Full-stack deployment (test host)

The Makefile targets assume a test host defined by `TEST_HOST` (default `ubuntu@10.11.3.190`).

```bash
# Sync source, build image on host, start/update the stack
make up

# Tail all logs
make logs

# Service status
make ps

# Tear everything down (volumes preserved)
make down
```

The Docker Compose stack (`deploy/docker/docker-compose.yml`) starts six services:

| Service | Image | Ports |
|---|---|---|
| etcd | quay.io/coreos/etcd:v3.5.15 | internal |
| postgres | postgres:16-alpine | internal |
| controller | pulsar:dev | 8080 · 9090 · 9091 |
| agent-compute | pulsar:dev | — |
| agent-network | pulsar:dev | host network |
| agent-storage | pulsar:dev | — |

The Postgres schema is applied automatically on first start via `deploy/docker/initdb/01_schema.sql`.

---

## Configuration reference

`deploy/docker/pulsar.docker.yaml` is the canonical config used inside Docker:

```yaml
controller:
  listen_http: ":8080"
  listen_grpc: ":9090"
  etcd:
    endpoints: ["etcd:2379"]
  postgres:
    dsn: "postgres://pulsar:pulsar@postgres/pulsar?sslmode=disable"
  jwt:
    secret: "pulsar-dev-change-in-production"
    expiry: "24h"

agent:
  controller_grpc: "controller:9090"
  compute:
    hypervisor_drivers: [kvm]
    image_cache_dir: "/var/lib/pulsar/images"
    instance_dir:    "/var/lib/pulsar/instances"
  network:
    external_interface: "ens3"
    overlay_interface:  "ens3"
    vxlan_port: 4789
  storage:
    backends:
      lvm:
        volume_group: "pulsar-vg"
        thin_pool:    "pulsar-pool"

logging:
  level: "info"
  format: "console"

telemetry:
  prometheus_listen: ":9091"
```

---

## pulsarctl

`pulsarctl` is the command-line client for the Pulsar REST API.

### Installation

```bash
# Build locally
make pulsarctl
cp bin/pulsarctl /usr/local/bin/

# Or on the test host (already built at)
/home/ubuntu/pulsar/bin/pulsarctl
```

### Authentication

```bash
# Log in — stores token in ~/.config/pulsar/config.json
pulsarctl login \
  --endpoint http://10.11.3.190:8080 \
  --email    admin@pulsar.dev \
  --password secret

# Log out
pulsarctl logout
```

Global flags available on every command:

| Flag | Description |
|---|---|
| `--endpoint` | Override stored API endpoint |
| `--token` | Override stored token |
| `--json` | Print raw JSON instead of a table |

---

### Compute

#### Instances

```bash
pulsarctl compute instances list
pulsarctl compute instances get      <id>
pulsarctl compute instances create \
    --name     web1 \
    --flavor   <flavor-id> \
    --image    <image-id> \
    --networks <network-id> \
    --hypervisor kvm          # kvm | lxd | lxc | containerd (default: kvm)

pulsarctl compute instances action   <id> start
pulsarctl compute instances action   <id> stop
pulsarctl compute instances action   <id> reboot
pulsarctl compute instances action   <id> hard-reboot
pulsarctl compute instances action   <id> console
pulsarctl compute instances delete   <id>
```

#### Flavors

```bash
pulsarctl compute flavors list
pulsarctl compute flavors get     <id>
pulsarctl compute flavors create  --name m1.small --vcpus 2 --ram 2048 --disk 20
pulsarctl compute flavors delete  <id>
```

#### Images

```bash
pulsarctl compute images list
pulsarctl compute images get      <id>
pulsarctl compute images create   --name ubuntu-24.04 \
                                  --url  https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img \
                                  --format qcow2     # qcow2 | raw | oci
pulsarctl compute images delete   <id>
```

#### Nodes

```bash
pulsarctl compute nodes
```

---

### Network

#### Networks

```bash
pulsarctl network networks list
pulsarctl network networks get     <id>
pulsarctl network networks create  --name internal [--shared]
pulsarctl network networks delete  <id>
```

#### Subnets

```bash
pulsarctl network subnets list
pulsarctl network subnets get      <id>
pulsarctl network subnets create   --name internal-sub \
                                   --network <network-id> \
                                   --cidr 192.168.100.0/24 \
                                   --ip-version 4
pulsarctl network subnets delete   <id>
```

#### Ports

```bash
pulsarctl network ports list
pulsarctl network ports get        <id>
pulsarctl network ports create     --network <network-id> [--name myport]
pulsarctl network ports delete     <id>
```

#### Routers

```bash
pulsarctl network routers list
pulsarctl network routers get           <id>
pulsarctl network routers create        --name gw1
pulsarctl network routers add-interface    <router-id> --subnet <subnet-id>
pulsarctl network routers remove-interface <router-id> --subnet <subnet-id>
pulsarctl network routers delete        <id>
```

#### Floating IPs

```bash
pulsarctl network floatingips list
pulsarctl network floatingips get         <id>
pulsarctl network floatingips create      --network <external-network-id>
pulsarctl network floatingips associate   <floatingip-id> --port <port-id>
pulsarctl network floatingips delete      <id>
```

#### Security Groups

```bash
pulsarctl network security-groups list
pulsarctl network security-groups get        <id>
pulsarctl network security-groups create     --name default [--description "..."]
pulsarctl network security-groups add-rule   <sg-id> \
    --protocol  tcp \
    --direction ingress \
    --remote-ip 0.0.0.0/0 \
    --port-min  22 \
    --port-max  22
pulsarctl network security-groups delete-rule <sg-id> <rule-id>
pulsarctl network security-groups delete      <id>
```

---

### Storage

#### Volumes

```bash
pulsarctl storage volumes list
pulsarctl storage volumes get      <id>
pulsarctl storage volumes create   --name data1 --size 50 [--type <volume-type-id>]
pulsarctl storage volumes action   <id> attach
pulsarctl storage volumes action   <id> detach
pulsarctl storage volumes action   <id> extend
pulsarctl storage volumes delete   <id>
```

#### Snapshots

```bash
pulsarctl storage snapshots list
pulsarctl storage snapshots get      <id>
pulsarctl storage snapshots create   --name snap1 --volume <volume-id>
pulsarctl storage snapshots delete   <id>
```

#### Volume Types

```bash
pulsarctl storage volume-types list
pulsarctl storage volume-types get     <id>
pulsarctl storage volume-types create  --name fast-lvm --backend lvm   # lvm | ceph | nfs
pulsarctl storage volume-types delete  <id>
```

---

### Projects & Users

```bash
# Projects
pulsarctl projects list
pulsarctl projects get    <id>
pulsarctl projects create --name myproject

# Users
pulsarctl projects users
pulsarctl projects create-user --email dev@example.com [--role member]
```

---

### Example workflow

```bash
# 1. Authenticate
pulsarctl login --endpoint http://10.11.3.190:8080 --email admin@pulsar.dev --password secret

# 2. Create a flavor and register an image
pulsarctl compute flavors create --name m1.small --vcpus 2 --ram 2048 --disk 20
FLAVOR_ID=$(pulsarctl compute flavors list --json | jq -r '.[0].id')

pulsarctl compute images create \
  --name ubuntu-24.04 \
  --url  https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img \
  --format qcow2
IMAGE_ID=$(pulsarctl compute images list --json | jq -r '.[0].id')

# 3. Create a network
pulsarctl network networks create --name internal
NET_ID=$(pulsarctl network networks list --json | jq -r '.[0].id')

pulsarctl network subnets create --name internal-sub \
  --network $NET_ID --cidr 192.168.100.0/24

# 4. Launch an instance
pulsarctl compute instances create \
  --name     web1 \
  --flavor   $FLAVOR_ID \
  --image    $IMAGE_ID \
  --networks $NET_ID

# 5. Check status
pulsarctl compute instances list
pulsarctl compute nodes
```

---

## API endpoints

The controller exposes a REST API under `/v1`.  All endpoints except `/healthz` and `POST /v1/auth/tokens` require a `Authorization: Bearer <token>` header.

| Method | Path | Description |
|---|---|---|
| GET | /healthz | Health check |
| POST | /v1/auth/tokens | Issue JWT |
| DELETE | /v1/auth/tokens | Revoke JWT |
| GET/POST | /v1/projects | List / create projects |
| GET/PATCH/DELETE | /v1/projects/{id} | Get / update / delete project |
| GET/POST | /v1/users | List / create users |
| GET/POST | /v1/compute/instances | List / create instances |
| GET/DELETE | /v1/compute/instances/{id} | Get / delete instance |
| POST | /v1/compute/instances/{id}/action | Instance action |
| GET/POST | /v1/compute/flavors | List / create flavors |
| GET/DELETE | /v1/compute/flavors/{id} | Get / delete flavor |
| GET/POST | /v1/compute/images | List / create images |
| GET/DELETE | /v1/compute/images/{id} | Get / delete image |
| GET | /v1/compute/nodes | List registered compute agents |
| GET/POST | /v1/network/networks | List / create networks |
| GET/POST | /v1/network/subnets | List / create subnets |
| GET/POST | /v1/network/ports | List / create ports |
| PATCH/DELETE | /v1/network/ports/{id} | Update / delete port |
| GET/POST | /v1/network/routers | List / create routers |
| PUT/DELETE | /v1/network/routers/{id}/interfaces | Add / remove interface |
| GET/POST | /v1/network/floatingips | List / allocate floating IPs |
| PATCH/DELETE | /v1/network/floatingips/{id} | Associate / release floating IP |
| GET/POST | /v1/network/security-groups | List / create security groups |
| POST | /v1/network/security-groups/{id}/rules | Add rule |
| DELETE | /v1/network/security-groups/{id}/rules/{rule_id} | Delete rule |
| GET/POST | /v1/storage/volumes | List / create volumes |
| POST | /v1/storage/volumes/{id}/action | Volume action |
| GET/POST | /v1/storage/snapshots | List / create snapshots |
| GET/POST | /v1/storage/volume-types | List / create volume types |

---

## Prometheus metrics

The controller exposes Prometheus metrics at `:9091/metrics`. Point your scrape config at:

```yaml
- job_name: pulsar
  static_configs:
    - targets: ["10.11.3.190:9091"]
```

---

## Project layout

```
.
├── cmd/
│   ├── pulsar/          # Server binary entrypoint
│   └── pulsarctl/       # CLI client
├── internal/
│   ├── agent/           # Agent runner + compute executor
│   │   └── compute/
│   │       └── libvirt/ # KVM driver (go-libvirt)
│   ├── api/             # HTTP middleware, response helpers
│   ├── config/          # Viper config loader
│   ├── controller/      # REST router, gRPC server, pillar handlers
│   │   ├── compute/
│   │   ├── identity/
│   │   ├── network/
│   │   ├── registry/    # Agent registry (etcd-backed)
│   │   └── storage/
│   ├── proto/           # .proto source files
│   └── store/
│       ├── etcd/        # etcd v3 client wrapper
│       └── postgres/    # pgx connection pool + migrations
├── gen/proto/           # Generated protobuf Go code
├── pkg/
│   ├── fsm/             # Generic finite state machine
│   ├── id/              # UUID generator
│   └── retry/           # Exponential backoff helper
└── deploy/
    └── docker/
        ├── Dockerfile
        ├── docker-compose.yml
        ├── pulsar.docker.yaml
        └── initdb/
            └── 01_schema.sql
```
