include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-module-basic-go
PKG_VERSION:=$(PACKAGE_VERSION)

PKG_FLAGS:=overwrite

# Place conditional checks EARLY - before variables that depend on them
ifneq ($(TOPDIR),)
	# Feed-specific settings (auto-clone from git)
	PKG_SOURCE_PROTO:=git
	PKG_SOURCE_URL:=https://github.com/OpenTollGate/tollgate-module-basic-go.git
	PKG_SOURCE_VERSION:=$(shell git rev-parse HEAD) # Use exact current commit
	PKG_MIRROR_HASH:=skip
else
	# SDK build context (local files)
	PKG_BUILD_DIR:=$(CURDIR)
endif

PKG_MAINTAINER:=Your Name <your@email.com>
PKG_LICENSE:=CC0-1.0
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

GO_PKG:=github.com/OpenTollGate/tollgate-module-basic-go

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

define Build/Prepare
	$(call Build/Prepare/Default)
	echo "DEBUG: Contents of go.mod after prepare:"
	cat $(PKG_BUILD_DIR)/go.mod
endef

define Build/Configure
endef

define Build/Compile
	cd $(PKG_BUILD_DIR) && \
	echo "DEBUG: GOARCH=$(GOARCH) GOMIPS=$(GOMIPS)" && \
	env GOOS=linux \
	GOARCH=$(GOARCH) \
	GOMIPS=$(GOMIPS) \
	go build -o $(PKG_NAME) main.go
endef

define Package/$(PKG_NAME)/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/$(PKG_NAME) $(1)/usr/bin/tollgate-basic
	
	# Init script
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/init.d/tollgate-basic $(1)/etc/init.d/
	
	# UCI defaults for configuration
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/99-tollgate-setup $(1)/etc/uci-defaults/

	# UCI defaults for config migration (runs before 99-tollgate-setup)
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/98-tollgate-config-migration-v0.0.1-to-v0.0.2-migration $(1)/etc/uci-defaults/98-tollgate-config-migration-v0.0.1-to-v0.0.2-migration
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration $(1)/etc/uci-defaults/99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/99-tollgate-config-migration-v0.0.3-to-v0.0.4-migration $(1)/etc/uci-defaults/99-tollgate-config-migration-v0.0.3-to-v0.0.4-migration
	
	# UCI defaults for random LAN IP
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/95-random-lan-ip $(1)/etc/uci-defaults/
	
	# UCI defaults for captive portal symlink
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/90-tollgate-captive-portal-symlink $(1)/etc/uci-defaults/


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
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/files/etc/crontabs/root $(1)/etc/crontabs/
	
	# Install backup configuration for sysupgrade
	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/files/lib/upgrade/keep.d/tollgate $(1)/lib/upgrade/keep.d/

	# Install control scripts
	$(INSTALL_DIR) $(1)/CONTROL
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/CONTROL/preinst $(1)/CONTROL/
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/CONTROL/postinst $(1)/CONTROL/
endef

# Update FILES declaration to include NoDogSplash files
FILES_$(PKG_NAME) += \
	/usr/bin/tollgate-basic \
	/etc/init.d/tollgate-basic \
	/etc/config/firewall-tollgate \
	/etc/modt/* \
	/etc/profile \
	/usr/local/bin/first-login-setup \
	/etc/uci-defaults/99-tollgate-setup \
	/etc/uci-defaults/98-tollgate-config-migration-v0.0.1-to-v0.0.2-migration \
	/etc/uci-defaults/99-tollgate-config-migration-v0.0.2-to-v0.0.3-migration \
	/etc/uci-defaults/99-tollgate-config-migration-v0.0.3-to-v0.0.4-migration \
	/etc/uci-defaults/95-random-lan-ip \
	/etc/uci-defaults/90-tollgate-captive-portal-symlink \
	/etc/tollgate/tollgate-captive-portal-site/* \
	/etc/crontabs/root \
	/lib/upgrade/keep.d/tollgate


$(eval $(call BuildPackage,$(PKG_NAME)))

# Print IPK path after successful compilation
PKG_FINISH:=$(shell echo "Successfully built: $(IPK_FILE)" >&2)
