#!/usr/bin/env bash
set -euo pipefail

# Install OVN on the host (Ubuntu 22.04/24.04)
apt-get update
apt-get install -y ovn-central ovn-host openvswitch-switch openvswitch-common

# Initialise OVN databases
ovn-nbctl init 2>/dev/null || true
ovn-sbctl init 2>/dev/null || true

# Enable and start services
systemctl enable --now openvswitch-switch ovn-central ovn-controller

# Listen on TCP for remote access (controller connects via TCP)
ovn-nbctl set-connection ptcp:6641:127.0.0.1
ovn-sbctl set-connection ptcp:6642:127.0.0.1

# Configure OVS for OVN
OVERLAY_IP=$(ip -4 addr show dev $(ip route | awk '/default/ {print $5; exit}') | awk '/inet / {print $2}' | cut -d/ -f1)
ovs-vsctl set Open_vSwitch . \
  external-ids:ovn-remote=tcp:127.0.0.1:6642 \
  external-ids:ovn-encap-type=geneve \
  external-ids:ovn-encap-ip="${OVERLAY_IP}"

# Create OVS integration bridge (managed by ovn-controller)
ovs-vsctl --may-exist add-br br-int
# Create external/provider bridge for VLAN networks
ovs-vsctl --may-exist add-br br-ex

systemctl restart ovn-controller

echo "OVN configured. NB DB: unix:/var/run/ovn/ovnnb_db.sock"
echo "Chassis name: $(hostname)"
echo "Overlay IP: ${OVERLAY_IP}"
