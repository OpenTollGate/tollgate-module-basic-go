include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-wrt
PKG_VERSION:=0.4.0
PKG_RELEASE:=1

PKG_SOURCE:=$(PKG_NAME)-$(PKG_VERSION).tar.gz
PKG_SOURCE_URL:=https://codeload.github.com/OpenTollGate/tollgate-module-basic-go/tar.gz/v$(PKG_VERSION)?
PKG_HASH:=9b987617a60745e3259d073db9115ebf86fc96b6b73341d92459b563108fb765

PKG_MAINTAINER:=TollGate <tollgate@tollgate.me>
PKG_LICENSE:=GPL-3.0-only
PKG_LICENSE_FILES:=LICENSE

# The Go module root is src/ inside the tarball (src/go.mod). golang-build.sh
# checks $(BUILD_DIR)/go.mod to enable module mode and copies files relative
# to BUILD_DIR into $GO_BUILD_DIR/src/$GO_PKG/. If PKG_BUILD_DIR points at the
# tarball root instead of src/, go.mod is invisible and every file gets an
# extra src/ prefix that breaks GO_PKG_BUILD_PKG resolution.
PKG_BUILD_DIR:=$(BUILD_DIR)/tollgate-module-basic-go-$(PKG_VERSION)/src

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=github.com/OpenTollGate/tollgate-module-basic-go
# src/go.mod declares module .../basic-go (so the main package is
# .../basic-go/cmd/tollgate-wrt). tollgate-cli is a SEPARATE module
# (src/cmd/tollgate-cli/go.mod = "tollgate-cli") and cannot be built in this
# module context — package it separately or merge its module later.
GO_PKG_BUILD_PKG:= \
  $(GO_PKG)/cmd/tollgate-wrt

# The cli package is a local replace submodule: go.mod declares
# module .../src/cli => ./cli, so its import path is .../src/cli.
# Using .../cli (without /src) would silently skip version injection.
GO_PKG_LDFLAGS_X:= \
  $(GO_PKG)/src/cli.Version=v$(PKG_VERSION) \
  $(GO_PKG)/src/cli.GitCommit=$(PKG_VERSION) \
  $(GO_PKG)/src/cli.BuildTime=$(SOURCE_DATE_EPOCH)

include $(INCLUDE_DIR)/package.mk
include ../../lang/golang/golang-package.mk

define Package/tollgate-wrt
  SECTION:=net
  CATEGORY:=Network
  TITLE:=TollGate - Cashu-powered WiFi Payment Gateway
  URL:=https://tollgate.me
  DEPENDS:=+nodogsplash +jq $(GO_ARCH_DEPENDS)
endef

define Package/tollgate-wrt/description
  TollGate turns an OpenWrt router into a Cashu-powered payment gateway
  for internet access. Customers pay in sats (time- or data-based); the
  router gates network access via NoDogSplash and sweeps balances to
  configured Lightning addresses.
endef

define Package/tollgate-wrt/conffiles
/etc/tollgate/
/etc/config/firewall-tollgate
endef

define Package/tollgate-wrt/preinst
#!/bin/sh

if [ -f /etc/tollgate/install.json ]; then
	CURRENT_TIMESTAMP=$$(date +%s)
	if ! jq ".install_time = $$CURRENT_TIMESTAMP" /etc/tollgate/install.json > /tmp/install.json.tmp; then
		echo "Error: Failed to update install_time using jq" >&2
		exit 1
	fi
	if ! mv /tmp/install.json.tmp /etc/tollgate/install.json; then
		echo "Error: Failed to move temporary file to /etc/tollgate/install.json" >&2
		exit 1
	fi
else
	mkdir -p /etc/tollgate
	CURRENT_TIMESTAMP=$$(date +%s)
	if ! echo "{\"install_time\": $$CURRENT_TIMESTAMP}" > /etc/tollgate/install.json; then
		echo "Error: Failed to create /etc/tollgate/install.json" >&2
		exit 1
	fi
fi

exit 0
endef

define Package/tollgate-wrt/postinst
#!/bin/sh

for script in /etc/uci-defaults/90-tollgate-captive-portal-symlink \
              /etc/uci-defaults/99-tollgate-setup; do
    if [ -x "$$script" ]; then
        "$$script" || echo "Warning: $$script exited with code $$?"
    fi
done

if [ -x /etc/init.d/tollgate-wrt ]; then
    /etc/init.d/tollgate-wrt enable 2>/dev/null || true
fi

exit 0
endef

define Package/tollgate-wrt/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/tollgate-wrt $(1)/usr/bin/

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) ./files/etc/init.d/tollgate-wrt $(1)/etc/init.d/

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) ./files/etc/uci-defaults/90-tollgate-captive-portal-symlink $(1)/etc/uci-defaults/
	$(INSTALL_BIN) ./files/etc/uci-defaults/99-tollgate-setup $(1)/etc/uci-defaults/

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_DATA) ./files/etc/config/firewall-tollgate $(1)/etc/config/

	$(INSTALL_DIR) $(1)/usr/local/bin
	$(INSTALL_BIN) ./files/usr/local/bin/first-login-setup $(1)/usr/local/bin/

	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) ./files/usr/bin/check_package_path $(1)/usr/bin/

	$(INSTALL_DIR) $(1)/etc/tollgate
	$(INSTALL_DIR) $(1)/etc/tollgate/ecash

	$(INSTALL_DIR) $(1)/etc/tollgate/tollgate-captive-portal-site
	$(CP) ./files/tollgate-captive-portal-site/* $(1)/etc/tollgate/tollgate-captive-portal-site/

	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) ./files/lib/upgrade/keep.d/tollgate $(1)/lib/upgrade/keep.d/

	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) ./files/etc/hotplug.d/iface/95-tollgate-restart $(1)/etc/hotplug.d/iface/
endef

$(eval $(call BuildPackage,tollgate-wrt))
