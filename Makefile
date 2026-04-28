# Root-level OpenWrt package recipe used by scripts/build-sdk-package.sh.
# CI and release packaging continue to use packaging/Makefile.
PKG_MAKEFILE_DIR:=$(dir $(abspath $(lastword $(MAKEFILE_LIST))))

include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-wrt
TOLLGATE_DISPLAY_VERSION:=$(if $(strip $(PACKAGE_VERSION)),$(PACKAGE_VERSION),0.0.0)

ifeq ($(CONFIG_USE_APK),y)
PKG_VERSION:=$(shell sh "$(PKG_MAKEFILE_DIR)packaging/normalize-apk-version.sh" "$(TOLLGATE_DISPLAY_VERSION)")
else
PKG_VERSION:=$(TOLLGATE_DISPLAY_VERSION)
endif

PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)-$(PKG_VERSION)
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

PKG_MAINTAINER:=TollGate <tollgate@tollgate.me>
PKG_LICENSE:=CC0-1.0
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

include $(INCLUDE_DIR)/package.mk

define Package/$(PKG_NAME)
	SECTION:=net
	CATEGORY:=Network
	TITLE:=TollGate Basic Module
	DEPENDS:=+nodogsplash +luci +jq
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
wait_for_iface br-private || echo "Note: br-private not found - this is expected on devices without a private network configured."

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
	rm -rf $(PKG_BUILD_DIR)
	mkdir -p $(PKG_BUILD_DIR)
	(cd $(PKG_MAKEFILE_DIR) && tar \
		--exclude='./.git' \
		--exclude='./build_dir' \
		--exclude='./staging_dir' \
		--exclude='./tmp' \
		--exclude='./dl' \
		--exclude='./bin' \
		--exclude='./src/tollgate-wrt' \
		--exclude='./src/cmd/tollgate-cli/tollgate' \
		--exclude='./.DS_Store' \
		-cf - .) | (cd $(PKG_BUILD_DIR) && tar -xf -)
endef

define Build/Configure
endef

define Build/Compile
	$(eval BUILD_TIME=$(shell date -u '+%Y-%m-%d %H:%M:%S UTC'))
	$(eval GIT_COMMIT=$(shell printf '%s\n' "$(TOLLGATE_DISPLAY_VERSION)" | grep -oE '[a-f0-9]{7}$$' || git -C "$(PKG_MAKEFILE_DIR)" rev-parse --short HEAD 2>/dev/null || echo "unknown"))
	$(eval VERSION_LDFLAGS=-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.Version=$(TOLLGATE_DISPLAY_VERSION)' \
		-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.GitCommit=$(GIT_COMMIT)' \
		-X 'github.com/OpenTollGate/tollgate-module-basic-go/src/cli.BuildTime=$(BUILD_TIME)')

	cd $(PKG_BUILD_DIR)/src && \
	env $(TOLLGATE_GO_BUILD_ENV) \
	go build -o $(PKG_BUILD_DIR)/$(PKG_NAME) -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)" main.go

	cd $(PKG_BUILD_DIR)/src/cmd/tollgate-cli && \
	env $(TOLLGATE_GO_BUILD_ENV) \
	go build -o $(PKG_BUILD_DIR)/tollgate -trimpath -ldflags="-s -w $(VERSION_LDFLAGS)"

	@if [ "$(USE_UPX)" = "1" ]; then \
		if which upx >/dev/null 2>&1; then \
			ls -lh $(PKG_BUILD_DIR)/$(PKG_NAME) $(PKG_BUILD_DIR)/tollgate; \
			upx $(UPX_FLAGS) $(PKG_BUILD_DIR)/$(PKG_NAME); \
			upx $(UPX_FLAGS) $(PKG_BUILD_DIR)/tollgate; \
			ls -lh $(PKG_BUILD_DIR)/$(PKG_NAME) $(PKG_BUILD_DIR)/tollgate; \
		else \
			echo "ERROR: USE_UPX=1 but upx not on PATH" >&2; \
			exit 1; \
		fi; \
	fi
endef

define Package/$(PKG_NAME)/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/$(PKG_NAME) $(1)/usr/bin/tollgate-wrt
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/tollgate $(1)/usr/bin/tollgate

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/packaging/files/etc/init.d/tollgate-wrt $(1)/etc/init.d/

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/packaging/files/etc/uci-defaults/90-tollgate-captive-portal-symlink $(1)/etc/uci-defaults/
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/packaging/files/etc/uci-defaults/99-tollgate-setup $(1)/etc/uci-defaults/

	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/packaging/files/etc/config/firewall-tollgate $(1)/etc/config/

	$(INSTALL_DIR) $(1)/usr/local/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/packaging/files/usr/local/bin/first-login-setup $(1)/usr/local/bin/

	$(INSTALL_DIR) $(1)/etc/tollgate
	$(INSTALL_DIR) $(1)/etc/tollgate/ecash

	$(INSTALL_DIR) $(1)/etc/tollgate/tollgate-captive-portal-site
	$(CP) $(PKG_BUILD_DIR)/packaging/files/tollgate-captive-portal-site/* $(1)/etc/tollgate/tollgate-captive-portal-site/

	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/packaging/files/usr/bin/check_package_path $(1)/usr/bin/

	$(INSTALL_DIR) $(1)/etc/crontabs

	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/packaging/files/lib/upgrade/keep.d/tollgate $(1)/lib/upgrade/keep.d/

	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/packaging/files/etc/hotplug.d/iface/95-tollgate-restart $(1)/etc/hotplug.d/iface/
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
