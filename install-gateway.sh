#!/bin/sh

# Script to start egress gateway pod

set -e

# Capture the usual signals and exit from the script
trap 'echo "INT received, simply exiting..."; exit 0' INT
trap 'echo "TERM received, simply exiting..."; exit 0' TERM
trap 'echo "HUP received, simply exiting..."; exit 0' HUP

# IPTABLES_BACKEND may be set to "legacy" (default) or "nft".  The
# "legacy" default should always work, until Linux is eventually
# modified not to support the legacy mode any more.
: ${IPTABLES_BACKEND:=legacy}
iptables=iptables-${IPTABLES_BACKEND}

: ${EGRESS_VXLAN_VNI:=4097}

: ${EGRESS_VXLAN_PORT:=4790}

if [ -z "$EGRESS_POD_IP" ]
then
    echo "EGRESS_POD_IP not defined."
    exit 1
fi
MAC=`echo $EGRESS_POD_IP | awk -F. '{printf "a2:2a:%02x:%02x:%02x:%02x", $1, $2, $3, $4}'`

echo Egress VXLAN VNI: $EGRESS_VXLAN_VNI  VXLAN PORT: $EGRESS_VXLAN_PORT VXLAN MAC: $MAC Pod IP: $EGRESS_POD_IP

echo Configure iptable rules
${iptables} -t nat -A POSTROUTING -j MASQUERADE

echo Configure vxlan tunnel device
ip link add vxlan0 type vxlan id $EGRESS_VXLAN_VNI dstport $EGRESS_VXLAN_PORT dev eth0
ip link set vxlan0 address $MAC
ip link set vxlan0 up

echo Configure network settings
echo 1 > /proc/sys/net/ipv4/ip_forward
echo 0 > /proc/sys/net/ipv4/conf/all/rp_filter
echo 0 > /proc/sys/net/ipv4/conf/vxlan0/rp_filter

echo Egress gateway pod is running...
tail -f /dev/null
