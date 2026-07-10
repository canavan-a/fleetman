#!/bin/sh
set -e

# Fleet Agent uninstall script
# Stops the service, removes the binary, config, and service registration.

INSTALL_DIR="/usr/local/bin"

echo "Uninstalling fleet-agent..."

# ---- detect init system (same logic as install) ----
detect_init() {
  if [ -d /run/systemd/system ]; then echo "systemd"; return; fi
  if command -v procd >/dev/null 2>&1; then echo "procd"; return; fi
  if [ -f /etc/openwrt_release ] && command -v ubus >/dev/null 2>&1; then echo "procd"; return; fi
  if command -v rc-service >/dev/null 2>&1; then echo "openrc"; return; fi
  if [ -f /run/runit.stopit ]; then echo "runit"; return; fi
  if command -v sv >/dev/null 2>&1; then
    PID1_NAME=$(cat /proc/1/comm 2>/dev/null || true)
    if [ "$PID1_NAME" = "runit" ]; then echo "runit"; return; fi
  fi
  if command -v s6-svc >/dev/null 2>&1; then echo "s6"; return; fi
  if [ -f /proc/1/comm ]; then
    PID1_NAME=$(cat /proc/1/comm 2>/dev/null || true)
    if [ "$PID1_NAME" = "init" ]; then
      if readlink /sbin/init 2>/dev/null | grep -q busybox; then echo "busybox"; return; fi
    fi
  fi
  echo "initd"
}

INIT=$(detect_init)

# ---- stop and deregister service ----
case "$INIT" in
  systemd)
    systemctl stop fleet-agent 2>/dev/null || true
    systemctl disable fleet-agent 2>/dev/null || true
    rm -f /etc/systemd/system/fleet-agent.service
    systemctl daemon-reload
    ;;
  openrc)
    rc-service fleet-agent stop 2>/dev/null || true
    rc-update del fleet-agent default 2>/dev/null || true
    rm -f /etc/init.d/fleet-agent
    ;;
  procd)
    /etc/init.d/fleet-agent stop 2>/dev/null || true
    /etc/init.d/fleet-agent disable 2>/dev/null || true
    rm -f /etc/init.d/fleet-agent
    ;;
  runit)
    sv stop fleet-agent 2>/dev/null || true
    rm -f /run/service/fleet-agent 2>/dev/null
    rm -f /var/service/fleet-agent 2>/dev/null
    rm -f /service/fleet-agent 2>/dev/null
    rm -rf /etc/sv/fleet-agent
    rm -rf /var/log/fleet-agent
    ;;
  s6)
    s6-svc -d /run/service/fleet-agent 2>/dev/null || true
    rm -f /run/service/fleet-agent 2>/dev/null
    rm -rf /etc/s6/services/fleet-agent
    s6-svscanctl -a /run/service 2>/dev/null || true
    ;;
  busybox)
    /etc/init.d/fleet-agent stop 2>/dev/null || true
    rm -f /etc/init.d/fleet-agent
    # Remove inittab entry
    if [ -f /etc/inittab ]; then
      sed -i '/fleet-agent/d' /etc/inittab 2>/dev/null || true
      kill -HUP 1 2>/dev/null || true
    fi
    ;;
  initd)
    service fleet-agent stop 2>/dev/null || true
    update-rc.d fleet-agent remove 2>/dev/null || true
    rm -f /etc/init.d/fleet-agent
    ;;
esac

# ---- remove binary ----
rm -f "${INSTALL_DIR}/fleet-agent"
rm -f "${INSTALL_DIR}/fleet-agent.old"

# ---- remove config ----
rm -rf /etc/fleet-agent

# ---- remove pidfile ----
rm -f /var/run/fleet-agent.pid

echo ""
echo "fleet-agent uninstalled."
echo "  removed: ${INSTALL_DIR}/fleet-agent"
echo "  removed: /etc/fleet-agent/"
echo "  removed: ${INIT} service registration"
