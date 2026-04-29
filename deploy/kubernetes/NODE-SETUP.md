# Pulsar on Kubernetes — Node Setup Guide

This document covers everything required to prepare bare-metal (or VM) Kubernetes nodes
for running the Pulsar IaaS platform via the Helm chart at
`deploy/kubernetes/helm/pulsar/`.

## Table of Contents

1. [Architecture overview](#1-architecture-overview)
2. [Do I need to install OVN/libvirt/etc on the nodes?](#2-do-i-need-to-install-ovnlibvirtetc-on-the-nodes)
3. [Cluster prerequisites](#3-cluster-prerequisites)
4. [Option A — Fully containerized (recommended)](#4-option-a--fully-containerized-recommended)
5. [Option B — Host-installed daemons](#5-option-b--host-installed-daemons)
6. [Build and push images](#6-build-and-push-images)
7. [Helm install](#7-helm-install)
8. [Label nodes and verify](#8-label-nodes-and-verify)
9. [Production hardening](#9-production-hardening)

---

## 1. Architecture overview

```
┌──────────────────────────────────────────────────────────┐
│  Kubernetes cluster                                       │
│                                                           │
│  ┌──────────────┐  ┌────────────┐  ┌─────────────────┐  │
│  │  Controller  │  │  etcd pod  │  │  postgres pod   │  │
│  │  Deployment  │  │ StatefulSet│  │  StatefulSet    │  │
│  └──────┬───────┘  └────────────┘  └─────────────────┘  │
│         │ gRPC                                            │
│  ┌──────▼────────────────────────────────────────────┐   │
│  │  Pulsar agent DaemonSets                          │   │
│  │  agent-compute  agent-network  agent-storage      │   │
│  └──────┬────────────────────────────────────────────┘   │
│         │ socket / hostPath                               │
│  ┌──────▼────────────────────────────────────────────┐   │
│  │  Infra DaemonSets (optional — enable in values)   │   │
│  │  infra-compute  infra-network  infra-storage      │   │
│  │  (libvirtd+iscsid) (OVS+OVN)  (tgtd)             │   │
│  └──────┬────────────────────────────────────────────┘   │
└─────────┼────────────────────────────────────────────────┘
          │ hostPath mounts
┌─────────▼────────────────────────────────────────────────┐
│  Host — only needs: Linux kernel with KVM/OVS modules    │
│  (Ubuntu 22.04+, Debian 12+, RHEL 9+)                    │
└──────────────────────────────────────────────────────────┘
```

---

## 2. Do I need to install OVN/libvirt/etc on the nodes?

**Short answer: No.** All userspace daemons can run in containers.

| Component | Needs host install? | Notes |
|---|---|---|
| Kernel modules (`kvm`, `openvswitch`, `iscsi_tcp`, `dm_thin_pool`) | **Yes** — but only the kernel module, not userspace packages. Loaded automatically by a privileged init container. Standard on Ubuntu 22.04+. | |
| `/dev/kvm` | **Yes** — provided by the `kvm` kernel module. Present on any CPU with virtualisation enabled. | |
| libvirtd / QEMU | No | Containerized by `infra-compute` DaemonSet |
| OVS (ovs-vswitchd, ovsdb-server) | No | Containerized by `infra-network` DaemonSet |
| OVN (ovn-controller, optional northd) | No | Containerized by `infra-network` DaemonSet |
| iscsid (iSCSI initiator) | No | Containerized inside `infra-compute` DaemonSet |
| tgtd (iSCSI target) | No | Containerized by `infra-storage` DaemonSet |
| LVM volume group + thin pool | **Yes** — one-time setup on the storage node's block device | Initial `pvcreate`/`vgcreate`/`lvcreate` only |

**Minimum node requirement** with the infra DaemonSets enabled:
- Linux kernel ≥ 5.15 (Ubuntu 22.04+) with KVM and OVS modules compiled in (default)
- CPU virtualisation enabled (VMX/SVM) on compute nodes
- A block device for LVM on storage nodes

---

## 3. Cluster prerequisites

### 3.1 Kubernetes version

Kubernetes **≥ 1.25** recommended.

### 3.2 Pod Security Admission

The infra and agent DaemonSets require `privileged` containers.

```bash
kubectl create namespace pulsar
kubectl label namespace pulsar \
  pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/warn=privileged
```

### 3.3 StorageClass

PVCs are created for etcd, PostgreSQL, and the controller image store.
At least one StorageClass must be available (e.g., `local-path`, Longhorn, Ceph RBD).

### 3.4 CPU virtualisation (compute nodes only)

```bash
# Must return > 0
egrep -c '(vmx|svm)' /proc/cpuinfo

# In a nested-VM environment (cloud VM), enable nested virt:
echo 'options kvm_intel nested=1' > /etc/modprobe.d/kvm.conf
modprobe -r kvm_intel && modprobe kvm_intel
```

---

## 4. Option A — Fully containerized (recommended)

Enable the infra DaemonSets in your `values.yaml` override. The chart handles
everything else — kernel modules are loaded by init containers.

### 4.1 One-time storage node setup

The only step that cannot be containerized is creating the LVM volume group on the
raw block device. Run this **once** on each storage node:

```bash
# Replace /dev/sdb with your block device.
pvcreate /dev/sdb
vgcreate pulsar-vg /dev/sdb
lvcreate -L 200G --thinpool pulsar-pool pulsar-vg
```

### 4.2 Build infra images

```bash
# From the repository root:
docker build -t <registry>/pulsar-infra-compute:latest \
  -f deploy/docker/Dockerfile.infra-compute .

docker build -t <registry>/pulsar-infra-network:latest \
  -f deploy/docker/Dockerfile.infra-network .

docker build -t <registry>/pulsar-infra-storage:latest \
  -f deploy/docker/Dockerfile.infra-storage .

docker push <registry>/pulsar-infra-compute:latest
docker push <registry>/pulsar-infra-network:latest
docker push <registry>/pulsar-infra-storage:latest
```

### 4.3 values.yaml additions

```yaml
infraCompute:
  enabled: true
  image:
    repository: <registry>/pulsar-infra-compute
    tag: latest

infraNetwork:
  enabled: true
  image:
    repository: <registry>/pulsar-infra-network
    tag: latest
  # Set true on the node that should run the OVN NB/SB databases + northd.
  # For single-node dev, set true. For multi-node, set up an external OVN
  # central and point ovnRemote at it (see section 9 — Production hardening).
  ovnNorthdEnabled: true
  ovnProviderBridge: "br-ex"
  ovnProviderIface: "eth1"    # NIC for external/provider traffic

infraStorage:
  enabled: true
  image:
    repository: <registry>/pulsar-infra-storage
    tag: latest
```

> **Note:** The infra DaemonSets write their sockets/state to hostPath directories
> (`/var/run/libvirt`, `/var/run/openvswitch`, `/var/run/ovn`).
> The agent DaemonSets mount the same hostPath locations, so they connect to the
> daemons started by the infra DaemonSets seamlessly.

---

## 5. Option B — Host-installed daemons

Skip the infra DaemonSets (`infraCompute.enabled=false`, etc.) and install the
daemons directly on each node. This is the traditional approach and may be preferred
when the node already runs these services for other workloads.

### 5.1 Compute node (libvirt + QEMU + iSCSI initiator)

```bash
apt-get update
apt-get install -y \
  libvirt-daemon-system libvirt-clients qemu-kvm qemu-utils \
  open-iscsi genisoimage

systemctl enable --now libvirtd iscsid

# Pulsar working directories
mkdir -p /var/lib/pulsar/images /var/lib/pulsar/instances
```

### 5.2 Network node (OVN + OVS)

```bash
apt-get update
apt-get install -y openvswitch-switch openvswitch-common ovn-host ovn-central

systemctl enable --now openvswitch-switch ovn-central ovn-controller

# Initialise databases
ovn-nbctl init 2>/dev/null || true
ovn-sbctl init 2>/dev/null || true

# Configure OVS for OVN (replace eth0 with your overlay interface)
OVERLAY_IP=$(ip -4 addr show dev eth0 | awk '/inet / {print $2}' | cut -d/ -f1)
ovs-vsctl set Open_vSwitch . \
  external-ids:ovn-remote=unix:/var/run/ovn/ovnsb_db.sock \
  external-ids:ovn-encap-type=geneve \
  external-ids:ovn-encap-ip="${OVERLAY_IP}" \
  external-ids:ovn-bridge-mappings=physnet1:br-ex

ovs-vsctl --may-exist add-br br-int
ovs-vsctl --may-exist add-br br-ex
ovs-vsctl --may-exist add-port br-ex eth1   # provider NIC

systemctl restart ovn-controller
```

### 5.3 Storage node (LVM + tgtd)

```bash
apt-get update
apt-get install -y lvm2 thin-provisioning-tools tgt

systemctl enable --now tgt

# Create the LVM volume group (one-time, replace /dev/sdb)
pvcreate /dev/sdb
vgcreate pulsar-vg /dev/sdb
lvcreate -L 200G --thinpool pulsar-pool pulsar-vg
```

### 5.4 Kernel modules (all nodes)

```bash
cat > /etc/modules-load.d/pulsar.conf <<'EOF'
iscsi_tcp
dm_thin_pool
openvswitch
nf_conntrack
br_netfilter
EOF
modprobe iscsi_tcp dm_thin_pool openvswitch nf_conntrack br_netfilter
```

---

## 6. Build and push images

Build the main Pulsar binary image (used by controller + all agent DaemonSets):

```bash
docker build -t <registry>/pulsar:<tag> -f deploy/docker/Dockerfile .
docker push <registry>/pulsar:<tag>
```

For local clusters:

```bash
# kind
kind load docker-image pulsar:dev --name <cluster-name>

# k3s (on the node)
docker save pulsar:dev | k3s ctr images import -
```

---

## 7. Helm install

### 7.1 Example values override

```yaml
# my-values.yaml
image:
  repository: <registry>/pulsar
  tag: "1.0.0"

controller:
  publicURL: "http://<lb-ip-or-hostname>:8080"

jwt:
  secret: "$(openssl rand -hex 32)"

postgres:
  auth:
    password: "<strong-password>"

agentCompute:
  emulator: "/usr/bin/qemu-system-x86_64"

agentNetwork:
  externalInterface: "eth0"
  overlayInterface: "eth0"
  physnetName: "physnet1"

agentStorage:
  lvm:
    volumeGroup: "pulsar-vg"
    thinPool: "pulsar-pool"

# Uncomment to containerize host daemons (Option A):
# infraCompute:
#   enabled: true
#   image:
#     repository: <registry>/pulsar-infra-compute
#     tag: "1.0.0"
# infraNetwork:
#   enabled: true
#   image:
#     repository: <registry>/pulsar-infra-network
#     tag: "1.0.0"
#   ovnNorthdEnabled: true
#   ovnProviderIface: "eth1"
# infraStorage:
#   enabled: true
#   image:
#     repository: <registry>/pulsar-infra-storage
#     tag: "1.0.0"
```

### 7.2 Install

```bash
helm install pulsar deploy/kubernetes/helm/pulsar \
  --namespace pulsar \
  --values my-values.yaml \
  --wait --timeout 10m
```

### 7.3 Upgrade

```bash
helm upgrade pulsar deploy/kubernetes/helm/pulsar \
  --namespace pulsar \
  --values my-values.yaml \
  --wait
```

---

## 8. Label nodes and verify

### 8.1 Apply node labels

```bash
kubectl label node <compute-node> pulsar.io/pillar=compute
kubectl label node <network-node> pulsar.io/pillar=network
kubectl label node <storage-node> pulsar.io/pillar=storage
```

### 8.2 Expected pods

```bash
kubectl get pods -n pulsar -o wide

# pulsar-controller-<hash>          1/1  Running
# pulsar-etcd-0                     1/1  Running
# pulsar-postgres-0                 1/1  Running
# pulsar-agent-compute-<hash>       1/1  Running  ← per compute node
# pulsar-agent-network-<hash>       1/1  Running  ← per network node
# pulsar-agent-storage-<hash>       1/1  Running  ← per storage node
# pulsar-infra-compute-<hash>       1/1  Running  ← if infraCompute.enabled
# pulsar-infra-network-<hash>       1/1  Running  ← if infraNetwork.enabled
# pulsar-infra-storage-<hash>       1/1  Running  ← if infraStorage.enabled
```

### 8.3 Verify agent registration

```bash
kubectl port-forward -n pulsar svc/pulsar-controller 8080:8080 &

TOKEN=$(curl -s -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@pulsar.dev","password":"admin"}' | jq -r .token)

curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/v1/agents | jq
```

---

## 9. Production hardening

### 9.1 Change default credentials immediately

The seed SQL sets `admin@pulsar.dev` / `admin`. Change via the API after first login.

### 9.2 Use pre-created Kubernetes Secrets

```bash
kubectl create secret generic pulsar-jwt \
  --from-literal=jwt-secret="$(openssl rand -hex 32)" -n pulsar

kubectl create secret generic pulsar-postgres \
  --from-literal=postgres-password="$(openssl rand -hex 32)" \
  --from-literal=postgres-dsn="postgres://pulsar:<pw>@pulsar-postgres/pulsar?sslmode=disable" \
  -n pulsar
```

```yaml
jwt:
  existingSecret: pulsar-jwt
postgres:
  auth:
    existingSecret: pulsar-postgres
```

### 9.3 External OVN central (multi-node)

For a production multi-node cluster, run the OVN NB/SB databases and northd on
dedicated controller VMs outside Kubernetes (or as a Deployment on a dedicated node).
Set `infraNetwork.ovnNorthdEnabled=false` and point `infraNetwork.ovnRemote` at the
external SB address:

```yaml
infraNetwork:
  ovnNorthdEnabled: false
  ovnRemote: "tcp:10.0.0.5:6642"
```

### 9.4 External etcd (HA)

```yaml
etcd:
  enabled: false
  external:
    endpoints:
      - "https://etcd-0.example.com:2379"
      - "https://etcd-1.example.com:2379"
      - "https://etcd-2.example.com:2379"
```

### 9.5 Prometheus

```yaml
telemetry:
  serviceMonitor:
    enabled: true
    labels:
      release: prometheus
    interval: "15s"
```

### 9.6 Ingress

```yaml
controller:
  service:
    type: LoadBalancer
  publicURL: "https://pulsar.example.com"
  oidcBaseURL: "https://pulsar.example.com"
```


This document covers everything required to prepare bare-metal (or VM) Kubernetes nodes
for running the Pulsar IaaS platform via the Helm chart at
`deploy/kubernetes/helm/pulsar/`.

## Table of Contents

1. [Architecture overview](#1-architecture-overview)
2. [Cluster prerequisites](#2-cluster-prerequisites)
3. [Compute node setup (KVM + libvirt + iSCSI)](#3-compute-node-setup)
4. [Network node setup (OVN/OVS)](#4-network-node-setup)
5. [Storage node setup (LVM + tgtd)](#5-storage-node-setup)
6. [Build and push the Pulsar image](#6-build-and-push-the-pulsar-image)
7. [Helm install](#7-helm-install)
8. [Label nodes and verify](#8-label-nodes-and-verify)
9. [Production hardening](#9-production-hardening)

---

## 1. Architecture overview

```
┌──────────────────────────────────────────────────────────┐
│  Kubernetes cluster                                       │
│                                                           │
│  ┌──────────────┐  ┌────────────┐  ┌─────────────────┐  │
│  │  Controller  │  │  etcd pod  │  │  postgres pod   │  │
│  │  Deployment  │  │ StatefulSet│  │  StatefulSet    │  │
│  └──────┬───────┘  └────────────┘  └─────────────────┘  │
│         │ gRPC                                            │
│  ┌──────▼────────────────────────────────────────────┐   │
│  │  DaemonSets (one pod per labelled node)           │   │
│  │                                                   │   │
│  │  agent-compute  agent-network  agent-storage      │   │
│  │  (privileged)   (hostNetwork)  (hostNetwork       │   │
│  │                                 + privileged)     │   │
│  └───────────────────────────────────────────────────┘   │
└──────────────────────────────────────────────────────────┘
         │ hostPath mounts
┌────────▼─────────────────────────────────────────────────┐
│  Host (bare-metal / VM)                                   │
│  libvirtd  qemu-kvm  iscsid  ovn-controller  tgtd  lvm   │
└──────────────────────────────────────────────────────────┘
```

Pulsar agents run **inside containers** but communicate with **host-side daemons**
(libvirt, OVN, tgtd) through `hostPath` socket/directory mounts.
This means every daemon listed above must be **running on the host** before the
corresponding DaemonSet pod is scheduled.

Nodes are assigned a single role via a Kubernetes label:

| Label | Role |
|---|---|
| `pulsar.io/pillar=compute` | KVM hypervisor node |
| `pulsar.io/pillar=network` | OVN/OVS gateway node |
| `pulsar.io/pillar=storage` | LVM/iSCSI storage node |

A physical node may carry multiple roles (e.g., `compute` + `network`); apply all
relevant labels.

---

## 2. Cluster prerequisites

### 2.1 Kubernetes version

Kubernetes **≥ 1.25** is required (DaemonSet `hostNetwork` + `hostPath` socket mounts
have been stable for much longer, but 1.25+ is recommended for security improvements).

### 2.2 Container runtime

**containerd ≥ 1.6** or **CRI-O ≥ 1.25**.  Docker-shim is not supported in modern k8s.

### 2.3 Pod Security Admission

The compute and storage agents require `privileged` containers.
The namespace where Pulsar runs must allow privileged pods.

```bash
# Option A — add the label to the namespace (built-in PSA):
kubectl create namespace pulsar
kubectl label namespace pulsar \
  pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/warn=privileged

# Option B — if you use OPA/Gatekeeper or Kyverno, add an exemption for the
# pulsar ServiceAccount before installing the chart.
```

### 2.4 StorageClass

The chart provisions PersistentVolumeClaims for etcd, PostgreSQL, and the image
store. You need at least one StorageClass available (or set
`etcd.persistence.enabled=false`, `postgres.persistence.enabled=false`).

For production deployments:

- etcd → fast local SSD StorageClass (e.g., `local-path`) with `ReadWriteOnce`
- postgres → ditto
- image-store → a `ReadWriteMany` StorageClass (NFS, CephFS, Longhorn RWX) is
  recommended so image uploads survive pod rescheduling

### 2.5 Kernel modules (all nodes)

```bash
# Load at boot via /etc/modules-load.d/
cat > /etc/modules-load.d/pulsar.conf <<'EOF'
iscsi_tcp
dm_thin_pool
dm_multipath
openvswitch
nf_conntrack
br_netfilter
EOF

modprobe iscsi_tcp dm_thin_pool openvswitch nf_conntrack br_netfilter
```

---

## 3. Compute node setup

Run the following steps on every node you intend to label `pulsar.io/pillar=compute`.

### 3.1 Enable hardware virtualisation

Verify KVM is available:

```bash
egrep -c '(vmx|svm)' /proc/cpuinfo   # must be > 0
ls /dev/kvm                           # must exist
```

If this is a nested-VM environment (e.g., testing inside a cloud VM), enable nested
virtualisation:

```bash
# Intel
echo 'options kvm_intel nested=1' > /etc/modprobe.d/kvm.conf
# AMD
echo 'options kvm_amd nested=1' > /etc/modprobe.d/kvm.conf
modprobe -r kvm_intel && modprobe kvm_intel   # or kvm_amd
```

### 3.2 Install QEMU/KVM and libvirt

```bash
# Ubuntu 22.04 / 24.04
apt-get update
apt-get install -y \
  qemu-kvm qemu-utils libvirt-daemon-system libvirt-clients \
  virtinst cpu-checker genisoimage

# Enable and start libvirtd (socket-activated is fine)
systemctl enable --now libvirtd

# Verify
virsh version
ls -la /var/run/libvirt/libvirt-sock   # must exist
```

### 3.3 QEMU emulator path

The default emulator is `/usr/bin/qemu-system-x86_64`.
Verify it exists and is executable:

```bash
which qemu-system-x86_64
/usr/bin/qemu-system-x86_64 --version
```

If it lives elsewhere (e.g., `/usr/bin/kvm`), set
`agentCompute.emulator` in your `values.yaml` override.

### 3.4 Create Pulsar working directories

```bash
mkdir -p /var/lib/pulsar/images
mkdir -p /var/lib/pulsar/instances
chmod 755 /var/lib/pulsar/images /var/lib/pulsar/instances
```

### 3.5 iSCSI initiator

Compute nodes mount volumes from storage nodes via iSCSI:

```bash
apt-get install -y open-iscsi
systemctl enable --now iscsid
```

Verify the initiator name:

```bash
cat /etc/iscsi/initiatorname.iscsi
# InitiatorName=iqn.1993-08.org.debian:01:<hostname>
```

Create the iSCSI config directory if missing:

```bash
mkdir -p /etc/iscsi
```

---

## 4. Network node setup

Run the following steps on every node you intend to label `pulsar.io/pillar=network`.

### 4.1 Install OVN and Open vSwitch

```bash
apt-get update
apt-get install -y \
  ovn-central ovn-host openvswitch-switch openvswitch-common

# Enable services
systemctl enable --now openvswitch-switch ovn-central ovn-controller
```

### 4.2 Initialise OVN databases

```bash
ovn-nbctl init 2>/dev/null || true
ovn-sbctl init 2>/dev/null || true
```

### 4.3 Configure OVN

```bash
# Determine the overlay/encapsulation IP (the IP of the interface used for VXLAN/Geneve)
OVERLAY_IF=eth0          # change to your overlay interface name
OVERLAY_IP=$(ip -4 addr show dev "${OVERLAY_IF}" | awk '/inet / {print $2}' | cut -d/ -f1)

ovs-vsctl set Open_vSwitch . \
  external-ids:ovn-remote=unix:/var/run/ovn/ovnsb_db.sock \
  external-ids:ovn-encap-type=geneve \
  external-ids:ovn-encap-ip="${OVERLAY_IP}"

# Create the OVS integration bridge (managed entirely by ovn-controller)
ovs-vsctl --may-exist add-br br-int

# Create the external/provider bridge for VLAN/flat networks
ovs-vsctl --may-exist add-br br-ex

# Attach the physical provider NIC to br-ex
# Replace 'eth1' with the NIC that carries provider/external traffic.
# WARNING: this will remove the IP from eth1 — set the provider IP on br-ex instead.
PROVIDER_IF=eth1
ovs-vsctl --may-exist add-port br-ex "${PROVIDER_IF}"

systemctl restart ovn-controller
```

### 4.4 Map the physical network

```bash
# Register the physical network mapping (physnet1 → br-ex).
# This must match agentNetwork.physnetName in values.yaml.
ovs-vsctl set Open_vSwitch . \
  external-ids:ovn-bridge-mappings=physnet1:br-ex
```

### 4.5 Verify sockets

The Pulsar network agent expects these sockets to be present on the host:

```bash
ls /var/run/ovn/ovnnb_db.sock        # OVN Northbound socket
ls /var/run/openvswitch/db.sock      # OVS database socket
```

If the OVN NB socket is missing, start `ovn-northd`:

```bash
systemctl enable --now ovn-northd
```

### 4.6 Note on multi-node OVN

In a production cluster the `ovn-central` service (NB/SB databases + northd) typically
runs on dedicated controller VMs outside Kubernetes, and compute/network nodes run only
`ovn-controller`. Configure `agentNetwork.ovnNBAddr` in `values.yaml` to point at your
central OVN NB address (e.g., `tcp:10.0.0.1:6641`).

---

## 5. Storage node setup

Run the following steps on every node you intend to label `pulsar.io/pillar=storage`.

### 5.1 Install LVM and tgt (iSCSI target)

```bash
apt-get update
apt-get install -y lvm2 thin-provisioning-tools tgt

systemctl enable --now tgt
```

### 5.2 Create the Pulsar LVM volume group

Replace `/dev/sdb` with the block device(s) you want Pulsar to manage.

```bash
# Create physical volume(s)
pvcreate /dev/sdb

# Create volume group (name must match agentStorage.lvm.volumeGroup in values.yaml)
vgcreate pulsar-vg /dev/sdb

# Create a thin-provisioning pool
# Adjust size to available VG space.
# The thin_pool name must match agentStorage.lvm.thinPool in values.yaml.
lvcreate -L 200G --thinpool pulsar-pool pulsar-vg
```

Verify:

```bash
vgs pulsar-vg
lvs pulsar-vg/pulsar-pool
```

### 5.3 LVM configuration

```bash
# Allow LVM to manage thin pools automatically.
# Disable udev_sync if running inside a container (backup original first).
sed -i 's/# udev_sync = 1/udev_sync = 0/' /etc/lvm/lvm.conf  || true
sed -i 's/udev_sync = 1/udev_sync = 0/'   /etc/lvm/lvm.conf  || true

# Create lock directory
mkdir -p /run/lock/lvm
```

### 5.4 Verify tgtd socket

```bash
tgtadm --lld iscsi --op show --mode target   # should print empty target list
ls /run/tgtd 2>/dev/null || true             # socket may appear here depending on distro
```

---

## 6. Build and push the Pulsar image

The DaemonSets and the controller Deployment all use the same container image.
Build it from the repository root:

```bash
# Build
docker build -t <registry>/pulsar:<tag> -f deploy/docker/Dockerfile .

# Push
docker push <registry>/pulsar:<tag>
```

If you are using a private registry, create an image pull secret and reference it in
`values.yaml`:

```bash
kubectl create secret docker-registry pulsar-registry \
  --docker-server=<registry> \
  --docker-username=<user> \
  --docker-password=<token> \
  -n pulsar

# values.yaml
# image:
#   pullSecrets:
#     - name: pulsar-registry
```

For local development with `kind` or `k3s`, load the image directly:

```bash
# kind
kind load docker-image pulsar:dev --name <cluster-name>

# k3s (on the node)
docker save pulsar:dev | k3s ctr images import -
```

---

## 7. Helm install

### 7.1 Create namespace

```bash
kubectl create namespace pulsar
kubectl label namespace pulsar \
  pod-security.kubernetes.io/enforce=privileged \
  pod-security.kubernetes.io/warn=privileged
```

### 7.2 Create a values override file

```yaml
# my-values.yaml
image:
  repository: <registry>/pulsar
  tag: "1.0.0"

controller:
  publicURL: "http://<node-ip-or-lb-hostname>:8080"
  oidcBaseURL: "https://pulsar.example.com"

jwt:
  secret: "<strong-random-secret>"   # openssl rand -hex 32

postgres:
  auth:
    password: "<strong-pg-password>"

etcd:
  persistence:
    storageClass: fast-ssd
    size: 10Gi

agentCompute:
  emulator: "/usr/bin/qemu-system-x86_64"
  imageCacheDir: "/var/lib/pulsar/images"
  instanceDir: "/var/lib/pulsar/instances"

agentNetwork:
  externalInterface: "eth0"
  overlayInterface: "eth0"
  ovnNBAddr: "unix:/var/run/ovn/ovnnb_db.sock"
  physnetName: "physnet1"
  ovsBridge: "br-int"

agentStorage:
  lvm:
    volumeGroup: "pulsar-vg"
    thinPool: "pulsar-pool"

logging:
  level: info
  format: json
```

### 7.3 Install

```bash
helm install pulsar deploy/kubernetes/helm/pulsar \
  --namespace pulsar \
  --values my-values.yaml \
  --wait --timeout 5m
```

### 7.4 Upgrade

```bash
helm upgrade pulsar deploy/kubernetes/helm/pulsar \
  --namespace pulsar \
  --values my-values.yaml \
  --wait
```

---

## 8. Label nodes and verify

### 8.1 Apply node labels

```bash
# Compute nodes
kubectl label node <compute-node-1> pulsar.io/pillar=compute
kubectl label node <compute-node-2> pulsar.io/pillar=compute

# Network nodes (can overlap with compute nodes)
kubectl label node <network-node-1> pulsar.io/pillar=network

# Storage nodes
kubectl label node <storage-node-1> pulsar.io/pillar=storage
```

### 8.2 Verify pods are running

```bash
kubectl get pods -n pulsar -o wide

# Expected pods:
# pulsar-controller-<hash>        1/1  Running  (Deployment)
# pulsar-etcd-0                   1/1  Running  (StatefulSet)
# pulsar-postgres-0               1/1  Running  (StatefulSet)
# pulsar-agent-compute-<hash>     1/1  Running  (DaemonSet, per compute node)
# pulsar-agent-network-<hash>     1/1  Running  (DaemonSet, per network node)
# pulsar-agent-storage-<hash>     1/1  Running  (DaemonSet, per storage node)
```

### 8.3 Check agent registration

Agents register with the controller immediately on startup. Verify from the controller logs:

```bash
kubectl logs -n pulsar deploy/pulsar-controller | grep -i "agent registered"
```

Or query the registry via the API:

```bash
kubectl port-forward -n pulsar svc/pulsar-controller 8080:8080 &

# List registered agents (requires a valid JWT)
TOKEN=$(curl -s -X POST http://localhost:8080/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"admin@pulsar.dev","password":"admin"}' | jq -r .token)

curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/v1/agents | jq
```

### 8.4 Smoke test — create an instance

```bash
# Using pulsarctl (built from the repo root: make pulsarctl)
export PULSAR_URL=http://localhost:8080
export PULSAR_TOKEN=$TOKEN

./bin/pulsarctl compute flavors list
./bin/pulsarctl compute instances create \
  --name test-vm \
  --flavor <flavor-id> \
  --image <image-id>
```

---

## 9. Production hardening

### 9.1 Change default credentials

```bash
# Change admin password immediately after first login.
# The seed SQL in deploy/docker/initdb/02_seed.sql sets admin@pulsar.dev / admin.
# Patch via API:
curl -s -X PUT http://localhost:8080/v1/identity/users/<admin-uuid>/password \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"password":"<new-strong-password>"}'
```

### 9.2 Use Kubernetes Secrets for sensitive values

Instead of embedding secrets in `values.yaml`, pre-create the Secrets and reference them:

```bash
# JWT secret
kubectl create secret generic pulsar-jwt \
  --from-literal=jwt-secret="$(openssl rand -hex 32)" \
  -n pulsar

# Postgres password
kubectl create secret generic pulsar-postgres \
  --from-literal=postgres-password="$(openssl rand -hex 32)" \
  --from-literal=postgres-dsn="postgres://pulsar:<password>@pulsar-postgres/pulsar?sslmode=disable" \
  -n pulsar
```

Then in `values.yaml`:

```yaml
jwt:
  existingSecret: pulsar-jwt
  existingSecretKey: jwt-secret

postgres:
  auth:
    existingSecret: pulsar-postgres
```

### 9.3 External etcd (HA)

For production, run a dedicated etcd cluster (3 or 5 nodes) outside Kubernetes and
point Pulsar at it:

```yaml
etcd:
  enabled: false
  external:
    endpoints:
      - "https://etcd-0.example.com:2379"
      - "https://etcd-1.example.com:2379"
      - "https://etcd-2.example.com:2379"
    tls:
      caCert: "/etc/etcd/ca.crt"
      cert:   "/etc/etcd/client.crt"
      key:    "/etc/etcd/client.key"
```

Mount certs into the controller pod via an additional `volumes`/`volumeMounts` override.

### 9.4 mTLS between services

The config supports `agent.tls` and `controller.etcd.tls` blocks.
Generate certs with your CA and mount them via secrets + additional volumeMounts in
your `values.yaml` override.

### 9.5 Prometheus scraping

Set `telemetry.serviceMonitor.enabled=true` to create a `ServiceMonitor` for
Prometheus Operator. Each pod exposes `:9091/metrics`.

```yaml
telemetry:
  serviceMonitor:
    enabled: true
    labels:
      release: prometheus   # match your Prometheus Operator selector
    interval: "15s"
```

### 9.6 Resource limits

Set resource requests and limits for all components to prevent noisy-neighbour issues:

```yaml
controller:
  resources:
    requests: { cpu: 500m, memory: 512Mi }
    limits:   { cpu: 2,    memory: 2Gi  }

agentCompute:
  resources:
    requests: { cpu: 200m, memory: 256Mi }
    limits:   { cpu: 1,    memory: 1Gi  }
```

### 9.7 Ingress

Expose the REST API through an Ingress or LoadBalancer Service by changing the
controller service type:

```yaml
controller:
  service:
    type: LoadBalancer   # or NodePort

  # Set this to the externally-reachable URL so OAuth2 redirect_uris work:
  publicURL: "https://pulsar.example.com"
  oidcBaseURL: "https://pulsar.example.com"
```

For Ingress (nginx example):

```yaml
# Not included in the chart — create separately:
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: pulsar-controller
  namespace: pulsar
  annotations:
    nginx.ingress.kubernetes.io/backend-protocol: "HTTP"
    nginx.ingress.kubernetes.io/proxy-body-size:  "0"   # unlimited for image uploads
spec:
  ingressClassName: nginx
  rules:
    - host: pulsar.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: pulsar-controller
                port:
                  name: http
```

Note: the gRPC port (`:9090`) must be accessible to agents but should NOT be exposed
via HTTP Ingress — use a LoadBalancer Service or direct NodePort for gRPC.
