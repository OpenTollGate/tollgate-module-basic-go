PKG_MAKEFILE_DIR:=$(dir $(abspath $(lastword $(MAKEFILE_LIST))))

include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-wrt
TOLLGATE_DISPLAY_VERSION:=$(if $(strip $(PACKAGE_VERSION)),$(PACKAGE_VERSION),0.0.0)

ifeq ($(CONFIG_USE_APK),y)
PKG_VERSION:=$(shell sh "$(PKG_MAKEFILE_DIR)normalize-apk-version.sh" "$(TOLLGATE_DISPLAY_VERSION)")
else
PKG_VERSION:=$(TOLLGATE_DISPLAY_VERSION)
endif

PKG_FLAGS:=overwrite

# The Go binaries are built natively by CI and staged into $(PKG_MAKEFILE_DIR)
# as tollgate-wrt (service) and tollgate (CLI). The SDK only packages them.
PREBUILT_BIN:=$(PKG_MAKEFILE_DIR)tollgate-wrt
PREBUILT_CLI:=$(PKG_MAKEFILE_DIR)tollgate

PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)-$(PKG_VERSION)

PKG_MAINTAINER:=TollGate <tollgate@tollgate.me>
PKG_LICENSE:=CC0-1.0
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

include $(INCLUDE_DIR)/package.mk

define Package/$(PKG_NAME)
	SECTION:=net
	CATEGORY:=Network
	TITLE:=TollGate Basic Module
	# DEPENDS:=+nodogsplash +luci +jq
	# Temporarily disabled: opkg can't resolve libc virtual dependency from feeds.
	# Runtime deps are guaranteed on all target routers. Re-enable once resolved.
	DEPENDS:=
	PROVIDES:=nodogsplash-files
	CONFLICTS:=
	REPLACES:=nodogsplash base-files
endef

define Package/$(PKG_NAME)/description
	TollGate Basic Module for OpenWrt
endef

define Package/$(PKG_NAME)/preinst
#!/bin/sh

if [ -f /etc/tollgate/install.json ]; then
	CURRENT_TIMESTAMP=$$(date +%s)
	if ! jq ".install_time = $$CURRENT_TIMESTAMP" /etc/tollgate/install.json > /tmp/install.json.tmp; then
		echo "Error: Failed to update install_time using jq" >&2
		echo "$$(date) - Error: Failed to update install_time using jq" >> /tmp/tollgate-setup.log
		exit 1
	fi
	if ! mv /tmp/install.json.tmp /etc/tollgate/install.json; then
		echo "Error: Failed to move temporary file to /etc/tollgate/install.json" >&2
		echo "$$(date) - Error: Failed to move temporary file to /etc/tollgate/install.json" >> /tmp/tollgate-setup.log
		exit 1
	fi
else
	mkdir -p /etc/tollgate
	CURRENT_TIMESTAMP=$$(date +%s)
	if ! echo "{\"install_time\": $$CURRENT_TIMESTAMP}" > /etc/tollgate/install.json; then
		echo "Error: Failed to create /etc/tollgate/install.json" >&2
		echo "$$(date) - Error: Failed to create /etc/tollgate/install.json" >> /tmp/tollgate-setup.log
		exit 1
	fi
	echo "$$(date) - install_time set to $$CURRENT_TIMESTAMP" >> /tmp/tollgate-setup.log
fi

exit 0
endef

define Package/$(PKG_NAME)/postinst
#!/bin/sh

echo "Running post-installation script: Starting postinst execution"
echo "Current working directory: $$(pwd)"
echo "Current timestamp: $$(date)"

wait_for_iface() {
    local iface="$$1" count=0
    while [ $$count -lt 15 ]; do
        [ -d "/sys/class/net/$$iface" ] && return 0
        sleep 1
        count=$$((count + 1))
    done
    echo "Warning: $$iface did not appear within 15 s" >&2
    return 1
}

for script in /etc/uci-defaults/90-tollgate-captive-portal-symlink \
              /etc/uci-defaults/99-tollgate-setup; do
    if [ -x "$$script" ]; then
        echo "Running $$script ..."
        "$$script" || echo "Warning: $$script exited with code $$?"
    fi
done

echo "Restarting network..."
/etc/init.d/network restart 2>/dev/null || true
wait_for_iface br-lan
wait_for_iface br-private || echo "Note: br-private not found — this is expected on devices without a private network configured."

echo "Reloading remaining services..."
wifi reload 2>/dev/null || true
/etc/init.d/firewall reload 2>/dev/null || true
/etc/init.d/dnsmasq restart 2>/dev/null || true
/etc/init.d/uhttpd restart 2>/dev/null || true
/etc/init.d/nodogsplash restart 2>/dev/null || true

if [ -x /etc/init.d/tollgate-wrt ]; then
    /etc/init.d/tollgate-wrt enable 2>/dev/null || true
    /etc/init.d/tollgate-wrt stop 2>/dev/null || true
    /etc/init.d/tollgate-wrt start 2>/dev/null || true
else
    echo "Warning: /etc/init.d/tollgate-wrt not found, skipping service start"
fi

echo "Post-installation script completed successfully"
exit 0
endef

define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
	[ -f "$(PREBUILT_BIN)" ] || { echo "ERROR: prebuilt binary not found at $(PREBUILT_BIN). CI should stage it before invoking the SDK." >&2; exit 1; }
	[ -f "$(PREBUILT_CLI)" ] || { echo "ERROR: prebuilt CLI not found at $(PREBUILT_CLI)." >&2; exit 1; }
	$(CP) $(PREBUILT_BIN) $(PKG_BUILD_DIR)/tollgate-wrt
	$(CP) $(PREBUILT_CLI) $(PKG_BUILD_DIR)/tollgate
	[ -f "$(PKG_MAKEFILE_DIR)LICENSE" ] && $(CP) $(PKG_MAKEFILE_DIR)LICENSE $(PKG_BUILD_DIR)/LICENSE || true
endef

define Build/Configure
endef

define Build/Compile
	@if [ "$(USE_UPX)" = "1" ]; then \
		if which upx >/dev/null 2>&1; then \
			ls -lh $(PKG_BUILD_DIR)/tollgate-wrt $(PKG_BUILD_DIR)/tollgate; \
			upx $(UPX_FLAGS) $(PKG_BUILD_DIR)/tollgate-wrt; \
			upx $(UPX_FLAGS) $(PKG_BUILD_DIR)/tollgate; \
			ls -lh $(PKG_BUILD_DIR)/tollgate-wrt $(PKG_BUILD_DIR)/tollgate; \
		else \
			echo "ERROR: USE_UPX=1 but upx not on PATH" >&2; \
			exit 1; \
		fi; \
	fi
endef

define Package/$(PKG_NAME)/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/tollgate-wrt $(1)/usr/bin/tollgate-wrt
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/tollgate $(1)/usr/bin/tollgate

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/etc/init.d/tollgate-wrt $(1)/etc/init.d/

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/etc/uci-defaults/90-tollgate-captive-portal-symlink $(1)/etc/uci-defaults/
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/etc/uci-defaults/99-tollgate-setup $(1)/etc/uci-defaults/

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_DATA) $(PKG_MAKEFILE_DIR)files/etc/config/firewall-tollgate $(1)/etc/config/

	$(INSTALL_DIR) $(1)/usr/local/bin
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/usr/local/bin/first-login-setup $(1)/usr/local/bin/

	$(INSTALL_DIR) $(1)/etc/tollgate
	$(INSTALL_DIR) $(1)/etc/tollgate/ecash

	$(INSTALL_DIR) $(1)/etc/tollgate/tollgate-captive-portal-site
	$(CP) $(PKG_MAKEFILE_DIR)files/tollgate-captive-portal-site/* $(1)/etc/tollgate/tollgate-captive-portal-site/

	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/usr/bin/check_package_path $(1)/usr/bin/

	$(INSTALL_DIR) $(1)/etc/crontabs

	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) $(PKG_MAKEFILE_DIR)files/lib/upgrade/keep.d/tollgate $(1)/lib/upgrade/keep.d/

	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/etc/hotplug.d/iface/95-tollgate-restart $(1)/etc/hotplug.d/iface/
endef

FILES_$(PKG_NAME) += \
	/usr/bin/tollgate-wrt \
	/usr/bin/tollgate \
	/etc/init.d/tollgate-wrt \
	/etc/config/firewall-tollgate \
	/usr/local/bin/first-login-setup \
	/etc/uci-defaults/90-tollgate-captive-portal-symlink \
	/etc/uci-defaults/99-tollgate-setup \
	/etc/tollgate/tollgate-captive-portal-site/* \
	/etc/crontabs/root \
	/lib/upgrade/keep.d/tollgate \
	/etc/hotplug.d/iface/95-tollgate-restart


$(eval $(call BuildPackage,$(PKG_NAME)))
