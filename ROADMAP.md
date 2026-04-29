# Pulsar Roadmap

Status as of 2026-04-10.

Legend: ✅ Done · 🔧 Partial / stubbed · ❌ Not started

---

## Identity

| Feature | Status | Notes |
|---|---|---|
| Multi-tenancy (projects) | ✅ | Full CRUD, JWT scoped to project |
| Users + roles | ✅ | Admin / member roles, per-project |
| JWT authentication | ✅ | Issue, validate, revoke; includes `org_id`, `org_role`, `jti` claims |
| Password hashing | ✅ | bcrypt at `CreateUser` and `CreateToken`; legacy `dev-nohash` rows still accepted |
| Token revocation | ✅ | `DELETE /auth/tokens` writes JTI to etcd with TTL; `AuthMiddleware` checks deny-list on every request |
| Admin RBAC enforcement | ✅ | `requireAdmin` helper; `UpdateProject`, `DeleteProject`, `UpdateUser` return 403 for non-admin callers |
| `UpdateUser` (role + password) | ✅ | `PATCH /users/{id}` — change role or reset password (admin only) |
| `UpdateProject` (rename) | ✅ | `PATCH /projects/{id}` — rename project (admin only) |
| SSH keypairs | ✅ | Generate RSA-4096 or import; injected via cloud-init; scoped per-user |
| **Organizations** | ✅ | `organizations` table; `GET/POST/PATCH/DELETE /v1/orgs`; all projects and users scoped to an org; built-in `pulsar` default org for service-provider operations |
| **Platform admin role** | ✅ | `org_role = platform_admin` on built-in seed user; `requirePlatformAdmin` guard; only platform admins can manage orgs |
| **org_id / org_role in JWT** | ✅ | `CreateToken` now embeds `org_id` and `org_role` claims; `AuthMiddleware` injects both into context |
| **OpenID Connect / SSO** | ✅ | `identity_providers` table; OIDC authorize + callback flow; auto-provisioning new users from IDP claims; `federated_identities` link table; CSRF state in etcd |
| **Bring-your-own IDP per org** | ✅ | `GET/POST/PATCH/DELETE /v1/orgs/{org_id}/idps`; org_admin-scoped; domain hint for email-based IDP discovery |
| **IDP lookup (public endpoint)** | ✅ | `GET /v1/auth/idps?email=...` — unauthenticated; returns enabled IDPs for an email address by user lookup or domain_hint match |
| **SSO login page** | ✅ | Two-step login: email entry → password + SSO buttons rendered from IDP lookup |
| **OIDC callback page** | ✅ | `/auth/callback` stores JWT from query param and redirects to dashboard |
| **`pulsarctl login sso`** | ✅ | Browser-based OIDC flow; ephemeral loopback server captures token; saves credentials |
| **Personal access tokens (PATs)** | ✅ | `personal_access_tokens` table; `pat_`-prefixed opaque tokens; bcrypt-hashed at rest; `GET/POST/DELETE /v1/auth/tokens/personal` |
| **PAT auth in middleware** | ✅ | `AuthMiddleware` detects `pat_` prefix → DB lookup + bcrypt compare; async `last_used_at` tracking |
| **`pulsarctl login token`** | ✅ | Stores a PAT as active credentials; validates against `/v1/projects` before saving |
| **Access Tokens UI page** | ✅ | Create (with expiry picker), list, revoke; one-time token reveal with clipboard copy |
| **Identity Providers UI page** | ✅ | `IdpPage` — CRUD for org IDPs; toggle enable/disable; domain hint |
| **Organizations UI page** | ✅ | `OrgsPage` — platform_admin view; create, suspend, delete orgs |
| **Project scope middleware** | ✅ | `ProjectScopeMiddleware` validates project membership + org boundary; platform_admin bypasses |
| Fine-grained RBAC | 🔧 | Coarse admin/member enforced on identity mutations; no per-resource ownership checks yet |

---

## Compute

| Feature | Status | Notes |
|---|---|---|
| Instance CRUD | ✅ | Create, list, get, delete |
| Instance lifecycle actions | ✅ | Start, stop, reboot, hard-reboot, console (VNC) |
| Instance FSM | ✅ | pending → scheduling → building → active → stopped → deleted → error |
| Instance error recovery | ✅ | Hard-reboot / rebuild from error state |
| Flavors | ✅ | Full CRUD; determines vCPU / RAM / disk sizing |
| Images | ✅ | Full CRUD; HTTP download with qcow2/raw/OCI format support |
| cloud-init user data | ✅ | Seed ISO injected at boot |
| SSH keypair injection | ✅ | Key names on CreateInstanceRequest; injected via cloud-init authorized_keys |
| Security group selection at launch | ✅ | Specified in launch request; applied to instance ports |
| KVM / libvirt driver | ✅ | Production-ready; configurable emulator path and domain type |
| Smart scheduler | ✅ | Least-loaded agent, hypervisor-type filtering |
| Node registration / heartbeat | ✅ | Agents self-register; heartbeat tracked in etcd |
| Configurable QEMU emulator | ✅ | `domain_type` and `emulator` config keys per agent |
| Floating IP on instance list | ✅ | Auto-resolved from ports and displayed in UI |
| Instance resize | ✅ | Flavor change via `POST /instances/{id}/resize`; libvirt hot-add vCPU/RAM (ACPI); config-only for stopped domains; UI resize dialog on instance detail page |
| Live migration | ✅ | `POST /instances/{id}/migrate`; libvirt `DomainMigratePerform3Params` with non-shared-disk copy; live and offline modes; URI constructed as `qemu+tcp://{host}/system` |
| LXD driver | 🔧 | Driver stub with interface stubs only; REST API client not integrated |
| LXC driver | 🔧 | Directory placeholder only; no implementation |
| containerd driver | 🔧 | Directory placeholder only; no implementation |
| HA instances (shared-storage evacuation) | ❌ | Not started |
| Flavor → hypervisor affinity | ✅ | `hypervisor_type` on flavor drives scheduler |
| Boot from volume | ✅ | `block_device_mappings` on CreateInstanceRequest; iSCSI-exports volume to compute node; `delete_on_terminate` support; UI volume picker in launch dialog |

---

## Network

| Feature | Status | Notes |
|---|---|---|
| Networks CRUD | ✅ | VXLAN, VLAN, flat |
| Subnets CRUD | ✅ | CIDR allocation, gateway, DNS; inline management on Networks page |
| Subnet allocation pools | ✅ | Optional `[start, end]` IP range constrains port/router/floating-IP allocation |
| Ports CRUD | ✅ | MAC + IP allocation, OVN LSP creation, port index |
| Routers (NAT gateways) | ✅ | Create, add/remove interfaces, SNAT |
| Floating IPs | ✅ | Allocate, associate with port, release |
| Security groups | ✅ | Full CRUD with default-SG auto-creation (atomic) |
| Security group rules | ✅ | Direction, protocol, port range, CIDR, remote-SG reference |
| Rule validation | ✅ | Protocol, port range, CIDR, mutual-exclusion checks |
| OVN / OVS integration | ✅ | Port groups, ACLs, address sets |
| DHCP / DNS | ✅ | OVN DHCP options; dnsmasq via OVN |
| External networks (provider) | ✅ | Flat network type; auto-provisioned localnet port; validated SNAT/DNAT |
| OVN routing (default route) | ✅ | Fixed: lr-route-add with explicit output port (lrp-ext-<id>); end-to-end validated |
| Software-defined networking (VXLAN/GENEVE) | ✅ | VXLAN implemented; GENEVE config-ready |
| FWaaS (firewall on routers) | ✅ | Full CRUD (`FirewallPolicy` + `FirewallRule`); one policy per router; OVN `lr-policy` sync via `network.router.firewall.sync` task; UI Firewall Policies page with rule builder |
| Port security (per-instance ACLs) | ✅ | Via security groups on ports |

---

## Storage

| Feature | Status | Notes |
|---|---|---|
| Volumes CRUD | ✅ | Create, list, get, delete |
| Volume actions | ✅ | Attach, detach, extend |
| Snapshots CRUD | ✅ | Create, list, get, delete |
| Volume types | ✅ | CRUD; selects backend driver |
| LVM backend | ✅ | Production-ready; thin-pool provisioning; `thin-provisioning-tools` in container image |
| iSCSI attachment | ✅ | LVM volumes exported via iSCSI and attached to instances |
| Volume agent affinity (`agent_id`) | ✅ | `agent_id` field on Volume tracks which storage node holds the LV; all operations (export, detach, extend, delete) route to the correct agent |
| Ghost volume reconciliation | ✅ | Reconciler detects etcd records with no backing LV and re-dispatches `volume.create` or marks volume `error` |
| Ceph / RADOS RBD | ❌ | Not started |
| NFS backend | ❌ | Not started |
| GlusterFS backend | ❌ | Not started |
| Volume migration (cross-backend) | ❌ | Not started |
| Boot from volume | ✅ | Available in launch dialog; volume picker with `delete_on_terminate` option |
| Volume QoS | ❌ | Not started |
| Multipath replication | ❌ | Not started |

---

## Web UI

| Feature | Status | Notes |
|---|---|---|
| Dashboard | ✅ | Summary counts and status |
| Instances list + launch dialog | ✅ | Select flavor, image, network, keypair, security groups, user data |
| Instance detail | ✅ | Actions, console, network interfaces, metadata |
| Security groups on instance | ✅ | View and edit per-port; full rule table with remote-SG display |
| Flavors page | ✅ | List, create, delete |
| Images page | ✅ | List, create, delete |
| Compute nodes page | ✅ | Live agent status |
| Key Pairs page | ✅ | Generate, import, copy fingerprint, download PEM |
| Networks page | ✅ | List, create, delete; expandable rows with inline subnet management |
| Ports page | ✅ | List, delete; SG column + per-port SG edit |
| Routers page | ✅ | List, create, add/remove interfaces, set gateway, delete |
| Floating IPs page | ✅ | List, allocate, associate, release |
| Security groups page | ✅ | Full CRUD; rule builder with direction/protocol/port/CIDR/ethertype |
| Remote SG rule support | ✅ | `--remote-sg` in CLI; `remote_group_id` in API and UI |
| Firewall Policies page | ✅ | List, create, edit, delete; inline rule builder (priority, action, protocol, CIDR, port range) |
| Instance resize dialog | ✅ | Flavor picker on instance detail page; dispatches live or offline resize |
| Boot from volume (launch dialog) | ✅ | Volume picker on launch page; mutually exclusive with image; `delete_on_terminate` checkbox |
| Volumes page | ✅ | List, create, actions, delete |
| Snapshots page | ✅ | List, create, delete |
| Volume types page | ✅ | List, create, delete |
| Projects page | ✅ | List, create |
| Users page | ✅ | List, create |
| Project switcher (top bar) | ✅ | Switch active project context from header |
| Organizations page | ✅ | platform_admin view; create, suspend, delete orgs |
| Identity Providers page | ✅ | org_admin CRUD for OIDC IDPs; enable/disable toggle; domain hint |
| Access Tokens page | ✅ | Self-service PAT management; expiry presets; one-time token reveal |
| SSO login flow | ✅ | Two-step login: email → IDP discovery → SSO buttons or password |
| OIDC callback page | ✅ | Receives token from IDP redirect; stores in local state; redirects to dashboard |

---

## Infrastructure & Operations

| Feature | Status | Notes |
|---|---|---|
| Single binary distribution | ✅ | `pulsar controller` / `pulsar agent --pillar=…` |
| Docker Compose deployment | ✅ | Full 6-container stack; health checks |
| Prometheus metrics endpoint | ✅ | `:9091/metrics` on controller |
| etcd state store | ✅ | All real-time resource state |
| PostgreSQL ledger | ✅ | Users, projects, quotas, audit; auto-migrated |
| gRPC agent↔controller streams | ✅ | Bidirectional task assignment and results |
| mTLS between services | 🔧 | TLS config struct exists; not enforced in dev |
| Kubernetes / Helm deployment | ❌ | Not started |
| systemd unit files | ✅ | `pulsar-controller.service` and `pulsar-agent-compute.service` in `deploy/systemd/` |
| Ansible compute node role | ✅ | Full role: packages, OVS/OVN, libvirt, LVM thin-pool, Docker, agent deployment |
| Host / hypervisor metrics export | ❌ | Not started |
| Audit logging | 🔧 | Postgres schema present; not written to consistently |
| Quota enforcement | 🔧 | Schema present; not enforced at API layer |
