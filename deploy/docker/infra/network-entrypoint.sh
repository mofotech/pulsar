#!/bin/sh
set -e

# Ensure runtime directories exist on the shared hostPath mounts.
mkdir -p /var/run/openvswitch /var/run/ovn /etc/openvswitch

# ── Open vSwitch ───────────────────────────────────────────────────────────────

# Initialise the config database on first boot.
if [ ! -f /etc/openvswitch/conf.db ]; then
    ovsdb-tool create /etc/openvswitch/conf.db \
        /usr/share/openvswitch/vswitch.ovsschema
fi

# Start ovsdb-server (detached).
ovsdb-server /etc/openvswitch/conf.db \
    --remote=punix:/var/run/openvswitch/db.sock \
    --remote=db:Open_vSwitch,Open_vSwitch,manager_options \
    --pidfile=/var/run/openvswitch/ovsdb-server.pid \
    --log-file=/dev/stdout \
    --detach

ovs-vsctl --no-wait init

# Start ovs-vswitchd (detached).
ovs-vswitchd unix:/var/run/openvswitch/db.sock \
    --pidfile=/var/run/openvswitch/ovs-vswitchd.pid \
    --log-file=/dev/stdout \
    --detach

# ── OVN chassis configuration ──────────────────────────────────────────────────

# OVN_REMOTE     — address of the OVN SB database, e.g.
#                  unix:/var/run/ovn/ovnsb_db.sock  (default, for co-located central)
#                  tcp:<ip>:6642                    (remote OVN central)
# OVN_ENCAP_IP   — IP used for Geneve/VXLAN tunnels, auto-detected if empty
# OVN_ENCAP_TYPE — geneve (default) or vxlan
# OVN_CHASSIS_NAME — OVN chassis name; defaults to node hostname (Downward API)
# OVN_BRIDGE_MAPPINGS — e.g. "physnet1:br-ex"
# OVN_PROVIDER_BRIDGE — external OVS bridge to create (e.g. br-ex)
# OVN_PROVIDER_IFACE  — physical NIC to attach to OVN_PROVIDER_BRIDGE

OVN_ENCAP_TYPE="${OVN_ENCAP_TYPE:-geneve}"
OVN_REMOTE="${OVN_REMOTE:-unix:/var/run/ovn/ovnsb_db.sock}"

if [ -z "${OVN_ENCAP_IP}" ]; then
    OVN_ENCAP_IP=$(ip -4 route get 1.0.0.0 2>/dev/null | awk '{for(i=1;i<=NF;i++) if ($i=="src") {print $(i+1); exit}}')
fi

ovs-vsctl set Open_vSwitch . \
    external-ids:ovn-remote="${OVN_REMOTE}" \
    external-ids:ovn-encap-type="${OVN_ENCAP_TYPE}" \
    external-ids:ovn-encap-ip="${OVN_ENCAP_IP}" \
    external-ids:hostname="${OVN_CHASSIS_NAME:-$(hostname)}"

if [ -n "${OVN_BRIDGE_MAPPINGS}" ]; then
    ovs-vsctl set Open_vSwitch . \
        external-ids:ovn-bridge-mappings="${OVN_BRIDGE_MAPPINGS}"
fi

# Ensure integration bridge exists (managed by ovn-controller).
ovs-vsctl --may-exist add-br "${OVS_BRIDGE:-br-int}"

# Create the external/provider bridge and attach the physical NIC if requested.
if [ -n "${OVN_PROVIDER_BRIDGE}" ]; then
    ovs-vsctl --may-exist add-br "${OVN_PROVIDER_BRIDGE}"
    if [ -n "${OVN_PROVIDER_IFACE}" ]; then
        ovs-vsctl --may-exist add-port "${OVN_PROVIDER_BRIDGE}" "${OVN_PROVIDER_IFACE}"
    fi
fi

# ── OVN central (NB/SB databases + northd) ────────────────────────────────────
# Set OVN_NORTHD=true on the node that should host the OVN control plane.
# In a multi-node cluster only ONE node should have this set.

if [ "${OVN_NORTHD:-false}" = "true" ]; then
    mkdir -p /var/lib/ovn

    if [ ! -f /var/lib/ovn/ovnnb_db.db ]; then
        ovsdb-tool create /var/lib/ovn/ovnnb_db.db \
            /usr/share/ovn/ovn-nb.ovsschema
    fi
    if [ ! -f /var/lib/ovn/ovnsb_db.db ]; then
        ovsdb-tool create /var/lib/ovn/ovnsb_db.db \
            /usr/share/ovn/ovn-sb.ovsschema
    fi

    ovsdb-server /var/lib/ovn/ovnnb_db.db /var/lib/ovn/ovnsb_db.db \
        --remote=punix:/var/run/ovn/ovnnb_db.sock \
        --remote=punix:/var/run/ovn/ovnsb_db.sock \
        --pidfile=/var/run/ovn/ovsdb-server-nb.pid \
        --log-file=/dev/stdout \
        --detach

    ovn-northd \
        --ovnnb-db=unix:/var/run/ovn/ovnnb_db.sock \
        --ovnsb-db=unix:/var/run/ovn/ovnsb_db.sock \
        --pidfile=/var/run/ovn/ovn-northd.pid \
        --log-file=/dev/stdout \
        --detach
fi

# ── OVN controller (runs on every chassis) ────────────────────────────────────
exec ovn-controller unix:/var/run/openvswitch/db.sock \
    --pidfile=/var/run/ovn/ovn-controller.pid \
    --log-file=/dev/stdout
