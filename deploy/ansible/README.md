# Pulsar Ansible Role

Provisions bare-metal (or VM) nodes to join a Pulsar cluster as **compute nodes**.

## What it does

1. Installs system packages: Docker, OVS, OVN-host, libvirt/QEMU, tgtd
2. Configures OVS + `ovn-controller` to connect to the cluster's OVN southbound DB
3. Sets up `br-ex` for external network / physnet mapping
4. Creates Pulsar data directories
5. Configures an LVM thin pool for VM disk storage
6. Drops a per-node `pulsar-agent.yaml` config
7. Starts `agent-compute`, `agent-network`, and `agent-storage` Docker containers

## Quick start

```bash
cd deploy/ansible

# 1. Copy and edit the inventory
cp inventory/hosts.yml.example inventory/hosts.yml

# 2. Copy and edit group vars
cp group_vars/compute_nodes.yml.example group_vars/compute_nodes.yml

# 3. Run
ansible-playbook -i inventory/hosts.yml site.yml
```

## Requirements

- Ansible ≥ 2.14
- Target nodes: Ubuntu 24.04 LTS
- SSH key access to target nodes (become: sudo)
- The Pulsar controller must already be running and reachable from the target nodes

## Role variables

See [`group_vars/compute_nodes.yml.example`](group_vars/compute_nodes.yml.example)
for all variables with documentation.

## Directory layout

```
deploy/ansible/
├── README.md
├── site.yml                        # Top-level playbook
├── inventory/
│   └── hosts.yml.example
├── group_vars/
│   └── compute_nodes.yml.example
└── roles/
    └── pulsar_compute_node/
        ├── defaults/main.yml
        ├── handlers/main.yml
        ├── tasks/
        │   ├── main.yml
        │   ├── packages.yml
        │   ├── ovs_ovn.yml
        │   ├── libvirt.yml
        │   ├── storage.yml
        │   ├── docker.yml
        │   └── pulsar_agents.yml
        └── templates/
            ├── pulsar-agent.yaml.j2
            └── docker-compose-agent.yml.j2
```
