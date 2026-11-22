# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Design
Provide an IaaS compute cloude service with Compute, Networking and Storage pillars.

- Identity service
  - Support for multi tenancy
  - Allow for IDP integration ( SSO with OpenID Connect )
  - A project can bring in their own IDP
  - Role based access controls

- Compute requirements
  - Support KVM, LXD/LXC hypervisors
  - Smart scheduling of workloads
  - Size compute instances via flavors to set sizing options of instances
  - Flavors should determine what type of hypervisor they are scheduled onto ( KVM, LXD, etc.. )
  - Compute instances should be resizable, migratable
  - Able to bootstrap compute instance configuration with cloud-init user data
  - HA instance flavors (If a compute node fails, evac HA flavored instances ( these require shared storage ))

- Network requirements
  - Implement full CRUD actions on networks, subnets, routers, floating ip's, security groups, and firewalls.
  - Software defined networking with OpenVswitch/OVN
  - Users should be able to create private networks on their projects ( VXLAN, GENEVE )
  - Networks could have more than 1 subnet ( create subnets from subnet pools )
  - The cloud provider should be able to supply the external networks, accessible to the tenants
    These will be used for the external side of a NAT gateway, as well as reserved/floating IP's.
  - Users should be able to create their own routers ( NAT gateways )
  - Port security with security groups or ACL's ( per instances security groups )
  - Project firewalls FWaaS. Firewalls on the NAT gateway routers with ingress and egress rules
  - Networks should provide DHCP and DNS ( dnsmasq/ovn )
  - Ability to create ports to attach to instances

- Storage requirements
  - Full CRUD actions on storage volumes.
  - Support for various storage provider LVM, NFS, GlusterFS, Ceph
  - iSCSI support ( Create volume on storage provider, attach to instance via iSCSI, ( Ceph should use RADOS/RBD ) )
  - Volumes should have the ability to be migrated across storage providers ( LVM -> Ceph, LVM -> 
  LVM, NFS -> LVM etc...)
  - Attach multiple volumes to instances
  - Boot instance from volume
  - Extend volume
  - Volume QoS
  - Multipathing ( Create replicated volumes with multipathing attached to compute node )

### Distribution
Pulsar should be distributed as a single binary all services can be run from one binary.
Deployment can be done via containers, or on Kubernetes with Helm. As well as non containers with systemd units and config files.

### Service comunication
All Pulsar to Pulsar services should have secure gRPC communication via mTLS.
All Pulsar services should expose a /metrics endpoint for prometheus scraping.
Pulsar should expose metrics of the underlying host/s and hypervisor services.

## Commands

```bash
# Build
make build          # Compile server binary → bin/pulsar
make pulsarctl      # Compile CLI → bin/pulsarctl
make proto          # Regenerate protobuf stubs from internal/proto/*.proto

# Test & Lint
make test           # Unit tests with race detector (integration tests skip)
make test-integration  # + integration tests against throwaway Postgres/etcd (docker-compose.test.yml)
make web-test       # Web UI tests (cd web && npm test); API mocked with MSW, unhandled requests fail
make lint           # golangci-lint
make test-remote    # Run tests on test host (TEST_HOST, set in gitignored local.mk)

# Local development infrastructure
make dev-up         # Start etcd + postgres in Docker
make dev-down       # Tear down dev infrastructure

# Database migrations (requires golang-migrate CLI)
make migrate-up
make migrate-down

# Full-stack deployment (docker-compose)
make up             # Deploy etcd, postgres, controller, 3 agents
make down
make logs
```

Run a single test: `go test -run TestName ./internal/controller/compute/`

Identity/auth code talks to Postgres and etcd directly, so it is tested with integration tests (`internal/testutil`: fresh migrated DB per test). Any schema change must go in a migration under `internal/store/postgres/migrations/` *and* in both initdb scripts (`deploy/docker/initdb/01_schema.sql`, Helm `configmap-initdb.yaml`); `TestInitdbAndMigrationsProduceSameSchema` fails on drift.

Releases: push a `vX.Y.Z` tag → `.github/workflows/release.yml` (GoReleaser binaries + GHCR images).

## Architecture

Pulsar is a lightweight IaaS orchestration platform — single binary that runs as either a **controller** (control plane) or an **agent** (data plane).

### Control Plane vs. Data Plane

The controller exposes:
- `:8080` — REST API (`/v1/...`) via chi router
- `:9090` — gRPC server (bidirectional streams for agent ↔ controller communication)
- `:9091` — Prometheus metrics

Agents connect to the controller via gRPC and are differentiated by `--pillar` flag: `compute`, `network`, or `storage`. Each agent registers via heartbeat and receives `TaskAssignment` messages. Results come back as `TaskResult` messages over the same stream.

### State Storage

- **etcd** — primary real-time state for instances, networks, volumes (keys like `/pulsar/compute/instances/<uuid>`)
- **PostgreSQL** — users, projects, quotas, audit logs; managed via migrations in `internal/store/postgres/migrations/`

### Request Lifecycle

1. `pulsarctl` CLI → `POST /v1/compute/instances` with JWT
2. Auth middleware validates JWT → handler calls service layer
3. Service stores state in etcd, scheduler picks least-loaded agent
4. Controller sends `TaskAssignment` to agent over gRPC stream
5. Agent dispatches to pluggable driver (e.g., libvirt for KVM), executes, sends `TaskResult`
6. Controller updates etcd state

### Key Package Roles

| Path | Purpose |
|------|---------|
| `cmd/pulsar/` | Server entrypoint (Cobra: `controller`, `agent`, `version` subcommands) |
| `cmd/pulsarctl/` | CLI client |
| `internal/controller/` | HTTP handlers, gRPC server, scheduler, agent registry |
| `internal/agent/` | Agent runtime, task dispatch, pluggable drivers |
| `internal/agent/compute/libvirt/` | KVM driver (only fully implemented compute driver) |
| `internal/proto/` | Protobuf definitions; generated code in `gen/proto/` |
| `internal/store/etcd/` | etcd v3 client wrapper |
| `internal/store/postgres/` | pgx connection pool + migrations |
| `internal/config/` | Viper-based YAML config loader (env var prefix: `PULSAR_`) |
| `pkg/fsm/` | Thread-safe FSM for instance lifecycle (`pending → scheduling → building → active → stopped → deleted`) |
| `pkg/retry/` | Exponential backoff helper |

### Agent Drivers

Compute drivers implement the interface in `internal/agent/compute/driver.go`. Only the `libvirt` (KVM) driver exists; LXD/LXC/containerd are planned but have no code. The controller rejects any `hypervisor_type` not in `supportedHypervisors` (`internal/controller/compute/types.go`) — add a type there only together with its agent driver. Network and storage agent implementations are similarly skeletal.

### Configuration

Config is loaded from a YAML file (path via `--config` flag or `PULSAR_CONFIG` env var). See `internal/config/config.go` for the full struct. Docker-specific config lives at `deploy/docker/pulsar.docker.yaml`.
