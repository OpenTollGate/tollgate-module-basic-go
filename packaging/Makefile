PKG_MAKEFILE_DIR:=$(dir $(abspath $(lastword $(MAKEFILE_LIST))))

include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-wrt
# PACKAGE_VERSION is the release version, which CI takes from the git tag; the
# tag must equal VERSION at the repository root (the single source of truth —
# see CONTRIBUTING.md, "Version single source of truth"). It becomes the package version and is substituted into the
# __TOLLGATE_VERSION__ placeholder shipped by files/etc/uci-defaults/99-tollgate-setup,
# exactly as the .ipk payload staging does.
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
PKG_LICENSE:=GPL-3.0-only
PKG_LICENSE_FILES:=LICENSE
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

include $(INCLUDE_DIR)/package.mk

# nodogsplash is a RUNTIME dependency, not a package this one supersedes: the
# module gates the network *through* the daemon and only ships files into its
# config/doc space. The defect was the missing DEPENDS -- and it showed on both
# lanes:
#
#   - apk lane (the SDK build we installed on hardware): the artifact carried
#     `depends:libc` and no `replaces:` field at all -- verified from the raw
#     `apk mkpkg` invocation in the build log -- so nothing pulled or retained
#     the daemon. After installing on a GL-MT3000 (OpenWrt 25.12.5) nodogsplash
#     was gone and the captive portal was down until it was reinstalled by
#     hand. Same failure class packaging/preinst already documents: an
#     undeclared runtime dependency that a maintainer script needs, ending in
#     the daemon being orphan-removed.
#   - opkg lane (.ipk): the recipes additionally stamped `Replaces: nodogsplash`
#     into the control file, where `Replaces` does supersede the named package.
#
# So: declare the daemon, never also claim to replace it. Same contract as the
# shipping-path feed definition, net/tollgate-wrt/Makefile
# (`DEPENDS:=+nodogsplash +jq`).
define Package/$(PKG_NAME)
	SECTION:=net
	CATEGORY:=Network
	TITLE:=TollGate Basic Module
	DEPENDS:=+nodogsplash +jq
	PROVIDES:=nodogsplash-files
	CONFLICTS:=
	REPLACES:=base-files
endef

define Package/$(PKG_NAME)/description
	TollGate Basic Module for OpenWrt
endef

define Package/$(PKG_NAME)/preinst
#!/bin/sh
# No-op: the binary owns install.json (config_manager.EnsureDefaultInstall)
# and stamps install_time itself. A jq-dependent preinst aborted upgrades on
# jq-less minimal systems (see Amperstrand/tollgate-module-basic-go#93).

exit 0

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
              /etc/uci-defaults/99-tollgate-setup \
              /etc/uci-defaults/92-tollgate-admin-setup; do
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
	$(SED) 's|__TOLLGATE_VERSION__|$(TOLLGATE_DISPLAY_VERSION)|g' $(1)/etc/uci-defaults/99-tollgate-setup
	# 92-tollgate-admin-setup (dedicated uhttpd instance on :8090) is staged by
	# packaging/portal-build.sh from the pinned portal tree with __ADMIN_HOME__
	# already substituted for this build's webroot, so it carries no placeholder.
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/etc/uci-defaults/92-tollgate-admin-setup $(1)/etc/uci-defaults/


	$(INSTALL_DIR) $(1)/usr/local/bin
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/usr/local/bin/first-login-setup $(1)/usr/local/bin/

	$(INSTALL_DIR) $(1)/etc/tollgate
	$(INSTALL_DIR) $(1)/etc/tollgate/ecash

	$(INSTALL_DIR) $(1)/etc/tollgate/tollgate-captive-portal-site
	$(CP) $(PKG_MAKEFILE_DIR)files/tollgate-captive-portal-site/* $(1)/etc/tollgate/tollgate-captive-portal-site/

	# Admin board (Preact SPA) from the same pinned portal commit: served at the
	# root of the dedicated uhttpd instance on :8090, from /www/tollgate.
	$(INSTALL_DIR) $(1)/www/tollgate
	$(CP) $(PKG_MAKEFILE_DIR)files/tollgate-admin/. $(1)/www/tollgate/

	# rpcd plugin + ACL backing the `tollgate` ubus object the board talks to.
	$(INSTALL_DIR) $(1)/usr/libexec/rpcd
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/usr/libexec/rpcd/tollgate $(1)/usr/libexec/rpcd/

	$(INSTALL_DIR) $(1)/usr/share/rpcd/acl.d
	$(INSTALL_CONF) $(PKG_MAKEFILE_DIR)files/usr/share/rpcd/acl.d/tollgate.json $(1)/usr/share/rpcd/acl.d/

	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/usr/bin/check_package_path $(1)/usr/bin/
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/usr/bin/tollgate-apply-ssl $(1)/usr/bin/
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/usr/bin/tollgate-remove-ssl $(1)/usr/bin/

	$(INSTALL_DIR) $(1)/etc/crontabs

	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) $(PKG_MAKEFILE_DIR)files/lib/upgrade/keep.d/tollgate $(1)/lib/upgrade/keep.d/

	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) $(PKG_MAKEFILE_DIR)files/etc/hotplug.d/iface/95-tollgate-restart $(1)/etc/hotplug.d/iface/

	$(INSTALL_DIR) $(1)/etc/nftables.d
	$(INSTALL_DATA) $(sort $(wildcard $(PKG_MAKEFILE_DIR)files/etc/nftables.d/*.nft)) $(1)/etc/nftables.d/

	$(INSTALL_DIR) $(1)/usr/share/man/man8
	$(INSTALL_DATA) $(PKG_MAKEFILE_DIR)files/man/man8/*.8 $(1)/usr/share/man/man8/
endef

FILES_$(PKG_NAME) += \
	/usr/bin/tollgate-wrt \
	/usr/bin/tollgate \
	/usr/bin/tollgate-apply-ssl \
	/usr/bin/tollgate-remove-ssl \
	/etc/init.d/tollgate-wrt \
	/usr/local/bin/first-login-setup \
	/etc/uci-defaults/90-tollgate-captive-portal-symlink \
	/etc/uci-defaults/99-tollgate-setup \
	/etc/uci-defaults/92-tollgate-admin-setup \
	/www/tollgate/* \
	/usr/libexec/rpcd/tollgate \
	/usr/share/rpcd/acl.d/tollgate.json \
	/etc/tollgate/tollgate-captive-portal-site/* \
	/etc/crontabs/root \
	/lib/upgrade/keep.d/tollgate \
	/etc/hotplug.d/iface/95-tollgate-restart \
	/etc/nftables.d/20-nds-enforce.nft


$(eval $(call BuildPackage,$(PKG_NAME)))
