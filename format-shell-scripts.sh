#!/bin/bash

# This script formats all shell scripts in the Tollgate project using shfmt.

for file in \
  files/etc/config/firewall-tollgate \
  files/etc/crontabs/root \
  files/etc/init.d/tollgate-basic \
  files/etc/uci-defaults/90-tollgate-captive-portal-symlink \
  files/etc/uci-defaults/95-random-lan-ip \
  files/etc/uci-defaults/98-tollgate-config-migration-v0.0.1-to-v0.0.2-migration \
  files/etc/uci-defaults/99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration \
  files/etc/uci-defaults/99-tollgate-setup \
  files/usr/bin/check_package_path \
  files/usr/local/bin/first-login-setup \
  files/CONTROL/preinst \
  files/CONTROL/postinst; do
  echo "Formatting $file..."
  shfmt -w "$file"
  if [ $? -ne 0 ]; then
    echo "Error formatting $file. Please ensure shfmt is installed (go install mvdan.cc/sh/v3/cmd/shfmt@latest)."
  fi
done

echo "Shell script formatting complete."