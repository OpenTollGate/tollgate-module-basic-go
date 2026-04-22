#!/bin/sh
# apply-ssl.sh - Configure HTTPS captive portal on a TollGate router
#
# Prerequisites:
#   - SSL cert files in this directory (tollgate.dns4sats.xyz.crt + .key)
#   - Router accessible via SSH
#
# Usage: ./apply-ssl.sh <router_ip> [ssh_password]
#
# What this does:
#   1. Deploys SSL cert to uhttpd
#   2. Enables uhttpd HTTPS on port 443
#   3. Configures dnsmasq to resolve tollgate.dns4sats.xyz to br-lan IP
#   4. Allows port 443 through nodogsplash firewall
#   5. Replaces nodogsplash splash.html with HTTPS redirect
#   6. Symlinks captive portal files into uhttpd webroot
#   7. Installs CGI proxy for mixed-content avoidance
#
# After running: connect a phone to the TollGate WiFi and verify the
# captive portal loads over HTTPS with a valid cert (camera/QR works).

set -e

ROUTER="${1:?Usage: $0 <router_ip> [ssh_password]}"
PASSWORD="${2:-c03rad0r123}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SSH="sshpass -p '$PASSWORD' ssh -o StrictHostKeyChecking=no root@$ROUTER"

echo "=== TollGate HTTPS Captive Portal Setup ==="
echo "Router: $ROUTER"

# 1. Deploy SSL cert
echo "[1/7] Deploying SSL certificate..."
sshpass -p "$PASSWORD" scp -o StrictHostKeyChecking=no \
    "$SCRIPT_DIR/tollgate.dns4sats.xyz.crt" \
    "root@$ROUTER:/etc/uhttpd.crt"
sshpass -p "$PASSWORD" scp -o StrictHostKeyChecking=no \
    "$SCRIPT_DIR/tollgate.dns4sats.xyz.key" \
    "root@$ROUTER:/etc/uhttpd.key"

# 2. Enable uhttpd HTTPS on port 443
echo "[2/7] Configuring uhttpd HTTPS..."
$SSH '
uci set uhttpd.main.listen_https="0.0.0.0:443 [::]:443"
uci set uhttpd.main.redirect_https="0"
uci commit uhttpd
'

# 3. Configure dnsmasq
echo "[3/7] Configuring DNS..."
$SSH '
sed -i "/tollgate.dns4sats.xyz/d" /etc/dnsmasq.conf
BR_LAN=$(uci get network.lan.ipaddr 2>/dev/null || echo "172.24.193.1")
echo "address=/tollgate.dns4sats.xyz/$BR_LAN" >> /etc/dnsmasq.conf
'

# 4. Allow port 443 in nodogsplash firewall
echo "[4/7] Opening port 443 in nodogsplash..."
$SSH '
uci get nodogsplash.@nodogsplash[0].users_to_router | grep -q "443" || \
    uci add_list nodogsplash.@nodogsplash[0].users_to_router="allow tcp port 443"
uci commit nodogsplash
'

# 5. Replace splash.html with HTTPS redirect
echo "[5/7] Installing splash redirect..."
$SSH '
PORTAL=/etc/tollgate/tollgate-captive-portal-site
cp "$PORTAL/splash.html" "$PORTAL/splash.html.pre-https" 2>/dev/null || true
cp "$PORTAL/splash.html" "$PORTAL/index.html"
cat > "$PORTAL/splash.html" << SPLASH
<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <title>Redirecting...</title>
  <script>
    window.location.replace("https://tollgate.dns4sats.xyz/");
  </script>
</head>
<body>
  <p>Redirecting to <a href="https://tollgate.dns4sats.xyz/">TollGate Portal</a>...</p>
</body>
</html>
SPLASH
'

# 6. Symlink portal files into uhttpd webroot
echo "[6/7] Symlinking portal files..."
$SSH '
PORTAL=/etc/tollgate/tollgate-captive-portal-site
for item in assets static locales favicon.ico manifest.json asset-manifest.json 404.html; do
    [ -e "$PORTAL/$item" ] && ln -sfn "$PORTAL/$item" "/www/$item"
done
# index.html for HTTPS root (the React app)
cp /www/index.html /www/index.html.openwrt-default 2>/dev/null || true
ln -sfn "$PORTAL/index.html" "/www/index.html"
'

# 7. Install CGI proxy
echo "[7/7] Installing CGI proxy..."
sshpass -p "$PASSWORD" scp -o StrictHostKeyChecking=no \
    "$SCRIPT_DIR/../../www/cgi-bin/tollgate-proxy" \
    "root@$ROUTER:/www/cgi-bin/tollgate-proxy"
$SSH 'chmod +x /www/cgi-bin/tollgate-proxy'

# 8. Patch React JS to use CGI proxy instead of direct HTTP
echo "[extra] Patching React app API calls..."
$SSH '
JS=/etc/tollgate/tollgate-captive-portal-site/assets/index-O5oIQeNS.js
if [ -f "$JS" ] && ! grep -q "tollgate-proxy" "$JS"; then
    cp "$JS" "$JS.original"
    sed -i "s|http://\${\$y}:2121|/cgi-bin/tollgate-proxy|g" "$JS"
    echo "  Patched API URL in React bundle"
else
    echo "  Already patched or JS not found"
fi
'

# Restart services
echo "Restarting services..."
$SSH '
/etc/init.d/uhttpd restart
/etc/init.d/dnsmasq restart
/etc/init.d/nodogsplash restart
'

echo ""
echo "=== Done! ==="
echo "Verify by connecting a phone to TollGate WiFi."
echo "The captive portal should load at https://tollgate.dns4sats.xyz/"
echo "Camera/QR scanning should work (secure context)."
