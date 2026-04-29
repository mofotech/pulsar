#!/bin/sh
set -e

# Ensure runtime directories exist on the shared hostPath mounts.
mkdir -p /var/run/libvirt /var/lib/libvirt/qemu

# Start virtlogd (background; captures guest serial/console output).
# Ignore errors if it is already running.
virtlogd --daemon 2>/dev/null || true

# Start iscsid if the host process is not already running.
# pgrep spans the host PID namespace because the pod sets hostPID: true.
if ! pgrep -x iscsid >/dev/null 2>&1; then
    iscsid -d0 &
fi

# Run libvirtd in the foreground so the container lifecycle matches the daemon.
exec libvirtd
