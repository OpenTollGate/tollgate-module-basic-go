include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-module-basic-go
# Use dynamic version generation like the main branch
PKG_VERSION:=$(shell git rev-list --count HEAD 2>/dev/null || echo "0.0.1").$(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
PKG_RELEASE:=1
PKG_FLAGS:=overwrite

# Place conditional checks EARLY - before variables that depend on them
ifneq ($(TOPDIR),)
	# Feed-specific settings (auto-clone from git)
	# Only use git if we're in a feed context and not in local development
	ifeq ($(findstring $(CURDIR),$(PKG_NAME)),)
		PKG_SOURCE_PROTO:=git
		PKG_SOURCE_URL:=https://github.com/OpenTollGate/tollgate-module-basic-go.git
		PKG_SOURCE_VERSION:=$(shell git rev-parse HEAD 2>/dev/null || echo "HEAD") # Use exact current commit
		PKG_MIRROR_HASH:=skip
	else
		# We're in OpenWrt build but with local files (SDK context)
		PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)
	endif
	# Auto create source tarball from git
	PKG_BUILD_DEPENDS:=golang/host
else
	# SDK build context (local files)
	PKG_BUILD_DIR:=$(CURDIR)
endif

PKG_MAINTAINER:=Your Name <your@email.com>
PKG_LICENSE:=CC0-1.0
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

# Go package configuration
GO_PKG:=github.com/OpenTollGate/tollgate-module-basic-go

# Include our local golang.mk instead of the system one
include $(CURDIR)/golang.mk

include $(INCLUDE_DIR)/package.mk
$(eval $(call GoPackage,$(PKG_NAME)))

define Package/$(PKG_NAME)
	SECTION:=net
	CATEGORY:=Network
	TITLE:=TollGate Basic Module
	DEPENDS:=$(GO_ARCH_DEPENDS) +nodogsplash +luci
	PROVIDES:=nodogsplash-files
	CONFLICTS:=
	PKG_ARCH:=all
	REPLACES:=nodogsplash base-files
endef

define Package/$(PKG_NAME)/description
	TollGate Basic Module for OpenWrt
endef

# Add fallback download method if the standard one fails
define Download/default
	FILE:=$(PKG_SOURCE)
	URL:=$(PKG_SOURCE_URL)
	SUBDIR:=$(PKG_SOURCE_SUBDIR)
	PROTO:=git
	VERSION:=$(PKG_SOURCE_VERSION)
endef

define Build/Prepare
	$(call Build/Prepare/Default)
	echo "DEBUG: Contents of go.mod after prepare:"
	cat $(PKG_BUILD_DIR)/go.mod
endef

define Build/Configure
endef

# Use GoPackage/Build/Compile from our local golang.mk
define Build/Compile
	$(call GoPackage/Build/Compile)
endef

define Package/$(PKG_NAME)/install
	# Install Go binary 
	$(call GoPackage/Package/Install,$(1))
	
	# Init script
	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/init.d/tollgate-basic $(1)/etc/init.d/ || true
	
	# UCI defaults for configuration
	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/99-tollgate-setup $(1)/etc/uci-defaults/ || true
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/95-random-lan-ip $(1)/etc/uci-defaults/ || true
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/etc/uci-defaults/90-tollgate-nodogsplash-files $(1)/etc/uci-defaults/ || true
	
	# Keep only TollGate-specific configs
	$(INSTALL_DIR) $(1)/etc/config
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/files/etc/config/firewall-tollgate $(1)/etc/config/ || true

	# First-login setup
	$(INSTALL_DIR) $(1)/usr/local/bin
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/usr/local/bin/first-login-setup $(1)/usr/local/bin/ || true
	
	# Create required directories
	$(INSTALL_DIR) $(1)/etc/tollgate
	$(INSTALL_DIR) $(1)/etc/tollgate/ecash
	$(INSTALL_DIR) $(1)/etc/nodogsplash/htdocs
	$(INSTALL_DIR) $(1)/etc/nodogsplash/htdocs/static/css
	$(INSTALL_DIR) $(1)/etc/nodogsplash/htdocs/static/js
	$(INSTALL_DIR) $(1)/etc/nodogsplash/htdocs/static/media

	# Tollgate config.json for mint and price
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/files/etc/tollgate/config.json $(1)/etc/tollgate/config.json || true
	
	# NoDogSplash files - copy what exists, ignore errors
	find $(PKG_BUILD_DIR)/files/etc/nodogsplash/htdocs -name "*.json" -exec $(INSTALL_DATA) {} $(1)/etc/nodogsplash/htdocs/ \; || true
	find $(PKG_BUILD_DIR)/files/etc/nodogsplash/htdocs -name "*.html" -exec $(INSTALL_DATA) {} $(1)/etc/nodogsplash/htdocs/ \; || true
	
	# Static files (CSS, JS, media) - using find to handle missing files
	find $(PKG_BUILD_DIR)/files/etc/nodogsplash/htdocs/static/css -type f -exec $(INSTALL_DATA) {} $(1)/etc/nodogsplash/htdocs/static/css/ \; || true
	find $(PKG_BUILD_DIR)/files/etc/nodogsplash/htdocs/static/js -type f -exec $(INSTALL_DATA) {} $(1)/etc/nodogsplash/htdocs/static/js/ \; || true
	find $(PKG_BUILD_DIR)/files/etc/nodogsplash/htdocs/static/media -type f -exec $(INSTALL_DATA) {} $(1)/etc/nodogsplash/htdocs/static/media/ \; || true
	
	# Install control scripts
	$(INSTALL_DIR) $(1)/CONTROL
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/CONTROL/preinst $(1)/CONTROL/ || true
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/files/CONTROL/postinst $(1)/CONTROL/ || true
endef

# Update FILES declaration to include NoDogSplash files
FILES_$(PKG_NAME) += \
	/usr/bin/tollgate-basic \
	/etc/init.d/tollgate-basic \
	/etc/config/firewall-tollgate \
	/etc/motd/* \
	/etc/profile \
	/usr/local/bin/first-login-setup \
	/etc/uci-defaults/99-tollgate-setup \
	/etc/uci-defaults/95-random-lan-ip \
	/etc/nodogsplash/htdocs/*.json \
	/etc/nodogsplash/htdocs/*.html \
	/etc/nodogsplash/htdocs/static/css/* \
	/etc/nodogsplash/htdocs/static/js/* \
	/etc/nodogsplash/htdocs/static/media/*


$(eval $(call BuildPackage,$(PKG_NAME)))

# Set IPK file path for debugging message
IPK_FILE := $(PACKAGE_DIR)/$(PKG_NAME)_$(PKG_VERSION)-$(PKG_RELEASE)_$(PKGARCH).ipk
# Print IPK path after successful compilation
PKG_FINISH := $(shell echo "Successfully built: $(IPK_FILE)" >&2)
