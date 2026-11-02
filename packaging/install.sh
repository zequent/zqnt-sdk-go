#!/bin/sh
# Installs zqnt-edge-node from an unpacked release archive: run as root from the archive's
# directory. An existing /etc/zqnt-edge-node/node.yaml is never overwritten.
set -eu

PREFIX=${PREFIX:-/usr/local}
USER_NAME=zqnt-edge
CONFIG_DIR=/etc/zqnt-edge-node
UNIT_DIR=/etc/systemd/system

cd "$(dirname "$0")"

if [ "$(id -u)" -ne 0 ]; then
  echo "install.sh must run as root" >&2
  exit 1
fi
for f in zqnt-edge-node node.yaml zqnt-edge-node.service; do
  if [ ! -f "$f" ]; then
    echo "missing $f: run install.sh from the unpacked release archive" >&2
    exit 1
  fi
done

if ! getent group "$USER_NAME" >/dev/null; then
  groupadd --system "$USER_NAME"
fi
if ! getent passwd "$USER_NAME" >/dev/null; then
  useradd --system --gid "$USER_NAME" --home-dir /var/lib/zqnt-edge-node --no-create-home \
    --shell /usr/sbin/nologin "$USER_NAME"
fi

install -D -m 0755 zqnt-edge-node "$PREFIX/bin/zqnt-edge-node"
install -d -m 0755 "$CONFIG_DIR"
if [ -e "$CONFIG_DIR/node.yaml" ]; then
  echo "kept existing $CONFIG_DIR/node.yaml"
else
  install -m 0640 -o root -g "$USER_NAME" node.yaml "$CONFIG_DIR/node.yaml"
  echo "installed example $CONFIG_DIR/node.yaml: set node_id and the link before starting"
fi

if [ "$PREFIX" = /usr/local ]; then
  install -m 0644 zqnt-edge-node.service "$UNIT_DIR/zqnt-edge-node.service"
else
  sed "s#/usr/local/bin/zqnt-edge-node#$PREFIX/bin/zqnt-edge-node#g" zqnt-edge-node.service \
    >"$UNIT_DIR/zqnt-edge-node.service"
  chmod 0644 "$UNIT_DIR/zqnt-edge-node.service"
fi
systemctl daemon-reload

echo "next: edit $CONFIG_DIR/node.yaml, check it with"
echo "  $PREFIX/bin/zqnt-edge-node --config $CONFIG_DIR/node.yaml --check-config"
echo "then: systemctl enable --now zqnt-edge-node"
