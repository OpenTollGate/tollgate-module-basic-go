PKG_MAKEFILE_DIR:=$(dir $(abspath $(lastword $(MAKEFILE_LIST))))

include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-wrt
TOLLGATE_PKG_SOURCE_URL?=https://github.com/OpenTollGate/tollgate-module-basic-go.git
TOLLGATE_DISPLAY_VERSION:=$(if $(strip $(PACKAGE_VERSION)),$(PACKAGE_VERSION),0.0.0)

ifeq ($(CONFIG_USE_APK),y)
PKG_VERSION:=$(shell sh "$(PKG_MAKEFILE_DIR)scripts/normalize-apk-version.sh" "$(TOLLGATE_DISPLAY_VERSION)")
else
PKG_VERSION:=$(TOLLGATE_DISPLAY_VERSION)
endif

PKG_FLAGS:=overwrite

TOLLGATE_GOARCH:=$(subst aarch64,arm64,$(subst i386,386,$(subst mips64el,mips64le,$(subst mipsel,mipsle,$(subst powerpc64,ppc64,$(subst x86_64,amd64,$(ARCH)))))))

ifeq ($(strip $(TOLLGATE_GOARCH)),)
TOLLGATE_GOARCH:=$(if $(strip $(GOARCH)),$(GOARCH),amd64)
endif

ifneq ($(filter $(TOLLGATE_GOARCH),mips mipsle),)
ifeq ($(CONFIG_HAS_FPU),y)
TOLLGATE_GOMIPS:=hardfloat
else
TOLLGATE_GOMIPS:=softfloat
endif
endif

TOLLGATE_GO_BUILD_ENV:=GOOS=linux GOARCH=$(TOLLGATE_GOARCH)

ifneq ($(strip $(TOLLGATE_GOMIPS)),)
TOLLGATE_GO_BUILD_ENV+=GOMIPS=$(TOLLGATE_GOMIPS)
endif

# Place conditional checks EARLY - before variables that depend on them
ifneq ($(TOPDIR),)
	# Feed-specific settings (auto-clone from git)
	PKG_SOURCE_PROTO:=git
	PKG_SOURCE_URL:=$(TOLLGATE_PKG_SOURCE_URL)
	PKG_SOURCE_VERSION:=$(shell git rev-parse HEAD) # Use exact current commit
	PKG_MIRROR_HASH:=skip
else
	# SDK build context (local files)
	PKG_BUILD_DIR:=$(CURDIR)
endif

PKG_MAINTAINER:=TollGate <tollgate@tollgate.me>
PKG_LICENSE:=CC0-1.0
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

GO_PKG:=github.com/OpenTollGate/tollgate-wrt

include $(INCLUDE_DIR)/package.mk
$(eval $(call GoPackage))

define Package/$(PKG_NAME)
	SECTION:=net
	CATEGORY:=Network
	TITLE:=TollGate Basic Module
	DEPENDS:=$(GO_ARCH_DEPENDS) +nodogsplash +luci +jq
	PROVIDES:=nodogsplash-files
	CONFLICTS:=
	REPLACES:=nodogsplash base-files
endef

define Package/$(PKG_NAME)/description
	TollGate Basic Module for OpenWrt
endef

define Package/$(PKG_NAME)/preinst
#!/bin/sh

# Check if /etc/tollgate/install.json exists
if [ -f /etc/tollgate/install.json ]; then
	# Update install_time in /etc/tollgate/install.json
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
	# Create /etc/tollgate/install.json if it doesn't exist
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

# Wait for a network interface to appear (max 15 s).
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

# Execute UCI defaults in lexical order so that wireless, network,
# firewall, and nodogsplash configurations are applied immediately
# without requiring a reboot.  Each script self-guards with a flag file
# so re-running is safe.
for script in /etc/uci-defaults/90-tollgate-captive-portal-symlink \
              /etc/uci-defaults/95-random-lan-ip \
              /etc/uci-defaults/99-tollgate-setup; do
    if [ -x "$$script" ]; then
        echo "Running $$script ..."
        "$$script" || echo "Warning: $$script exited with code $$?"
    fi
done

# Restart the network stack so new interfaces / bridges (e.g. br-private)
# are created.  Then wait for the bridges before bringing up wireless —
# wifi-ifaces that reference a non-existent bridge will silently fail.
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

# Start the TollGate service last — it depends on the config above.
# Use 'start' (not 'restart') because on a fresh install the service
# was never running.  procd's respawn will handle subsequent crashes.
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
	$(call Build/Prepare/Default)
	echo "DEBUG: Contents of go.mod after prepare:"
	cat $(PKG_BUILD_DIR)/go.mod
endef

define Build/Configure
endef

define Build/Compile
	# Set build variables
	$(eval BUILD_TIME=$(shell date -u '+%Y-%m-%d %H:%M:%S UTC'))
	# Prefer the original version string for binary metadata, even if apk packaging needs a sanitized package version.
	$(eval GIT_COMMIT=$(shell printf '%s\n' "$(TOLLGATE_DISPLAY_VERSION)" | grep -oE '[a-f0-9]{7}$$' || printf '%s\n' "$(PKG_SOURCE_VERSION)" | grep -oE '^[a-f0-9]{7}' || echo "unknown"))
	$(eval VERSION_LDFLAGS=-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.Version=$(TOLLGATE_DISPLAY_VERSION)' \
		-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.GitCommit=$(GIT_COMMIT)' \
		-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.BuildTime=$(BUILD_TIME)')
	
	cd $(PKG_BUILD_DIR) && \
	echo "DEBUG: TargetArch=$(ARCH) PackageArch=$(ARCH_PACKAGES) GoArch=$(TOLLGATE_GOARCH) GoMips=$(TOLLGATE_GOMIPS)" && \
	echo "DEBUG: PackageVersion=$(PKG_VERSION) DisplayVersion=$(TOLLGATE_DISPLAY_VERSION) Commit=$(GIT_COMMIT) BuildTime=$(BUILD_TIME)" && \
	env $(TOLLGATE_GO_BUILD_ENV) \
	go build -o $(PKG_NAME) -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" main.go
	
	# Build CLI tool
	cd $(PKG_BUILD_DIR)/src/cmd/tollgate-cli && \
	env $(TOLLGATE_GO_BUILD_ENV) \
	go build -o tollgate -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)"

	# Compress binaries with UPX if USE_UPX is enabled
	@if [ "$(USE_UPX)" = "1" ]; then \
		if which upx >/dev/null 2>&1; then \
			ls -lh $(PKG_BUILD_DIR)/$(PKG_NAME) $(PKG_BUILD_DIR)/src/cmd/tollgate-cli/tollgate; \
			upx $(UPX_FLAGS) $(PKG_BUILD_DIR)/$(PKG_NAME); \
			upx $(UPX_FLAGS) $(PKG_BUILD_DIR)/src/cmd/tollgate-cli/tollgate; \
			ls -lh $(PKG_BUILD_DIR)/$(PKG_NAME) $(PKG_BUILD_DIR)/src/cmd/tollgate-cli/tollgate; \
		fi; \
	fi
endef

define Package/$(PKG_NAME)/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/$(PKG_NAME) $(1)/usr/bin/tollgate-wrt
	
	# Install CLI tool in system PATH
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/src/cmd/tollgate-cli/tollgate $(1)/usr/bin/tollgate
	
	# Init script
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/init.d/tollgate-wrt $(1)/etc/init.d/
	
	# UCI defaults (run lexically on first boot)
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/90-tollgate-captive-portal-symlink $(1)/etc/uci-defaults/
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/95-random-lan-ip $(1)/etc/uci-defaults/
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/99-tollgate-setup $(1)/etc/uci-defaults/


	# Keep only TollGate-specific configs
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/files/etc/config/firewall-tollgate $(1)/etc/config/

	# First-login setup
	$(INSTALL_DIR) $(1)/usr/local/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/usr/local/bin/first-login-setup $(1)/usr/local/bin/
	
	# Create required directories
	$(INSTALL_DIR) $(1)/etc/tollgate
	$(INSTALL_DIR) $(1)/etc/tollgate/ecash
	
	# TollGate captive portal site files (will be symlinked by nodogsplash)
	$(INSTALL_DIR) $(1)/etc/tollgate/tollgate-captive-portal-site
	$(CP) $(PKG_BUILD_DIR)/files/tollgate-captive-portal-site/* $(1)/etc/tollgate/tollgate-captive-portal-site/
	
	# Install check_package_path script
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/usr/bin/check_package_path $(1)/usr/bin/

	# Install cron table
	$(INSTALL_DIR) $(1)/etc/crontabs
	
	# Install backup configuration for sysupgrade
	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/files/lib/upgrade/keep.d/tollgate $(1)/lib/upgrade/keep.d/

	# Install hotplug script for wan interface restart
	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/hotplug.d/iface/95-tollgate-restart $(1)/etc/hotplug.d/iface/
endef

# Update FILES declaration to include NoDogSplash files
FILES_$(PKG_NAME) += \
	/usr/bin/tollgate-wrt \
	/etc/init.d/tollgate-wrt \
	/etc/config/firewall-tollgate \
	/etc/modt/* \
	/etc/profile \
	/usr/local/bin/first-login-setup \
	/etc/uci-defaults/90-tollgate-captive-portal-symlink \
	/etc/uci-defaults/95-random-lan-ip \
	/etc/uci-defaults/99-tollgate-setup \
	/etc/tollgate/tollgate-captive-portal-site/* \
	/etc/crontabs/root \
	/lib/upgrade/keep.d/tollgate \
	/etc/hotplug.d/iface/95-tollgate-restart


$(eval $(call BuildPackage,$(PKG_NAME)))

# Print IPK path after successful compilation
PKG_FINISH:=$(shell echo "Successfully built: $(IPK_FILE)" >&2)
