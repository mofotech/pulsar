#!/bin/sh
set -e

# tgtd binds on the real host IP:3260 for iSCSI.
# Run in foreground (-f) so the container lifecycle matches the daemon.
exec tgtd -f
