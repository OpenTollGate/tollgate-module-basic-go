include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-module-basic-go

# Dynamic version generation
define Package/$(PKG_NAME)/GetGitInfo
    cd $(CURDIR) && \
    PKG_BRANCH=$$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "main") && \
    PKG_COMMITS=$$(git rev-list --count HEAD 2>/dev/null || echo "1") && \
    PKG_SHORT_COMMIT=$$(git rev-parse --short HEAD 2>/dev/null || echo "unknown") && \
    if [ "$$PKG_BRANCH" = "main" ] || [ "$$PKG_BRANCH" = "master" ]; then \
        echo "0.$$PKG_COMMITS" ; \
    else \
        echo "0.$$PKG_COMMITS-$$PKG_BRANCH" ; \
    fi
endef

PKG_VERSION:=$(shell $(call Package/$(PKG_NAME)/GetGitInfo))
PKG_RELEASE:=$(shell cd $(CURDIR) 2>/dev/null && git rev-parse --short HEAD 2>/dev/null || echo "1")

PKG_MAINTAINER:=Your Name <your@email.com>
PKG_LICENSE:=CC0-1.0
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_USE_MIPS16:=0

GO_PKG:=https://github.com/OpenTollGate/tollgate-module-basic-go.git
GO_PKG_BUILD_PKG:=$(GO_PKG)

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/$(PKG_NAME)
	SECTION:=net
	CATEGORY:=Network
	TITLE:=TollGate Whoami Module
	DEPENDS:=$(GO_ARCH_DEPENDS)
endef

define Package/go-example/description
	An example package in Golang.
endef

define Build/Prepare
	$(CP) ./src/* $(PKG_BUILD_DIR)
endef

define Package/$(PKG_NAME)/install
	$(INSTALL_DIR) $(1)/usr/sbin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/go-example $(1)/usr/sbin
endef

$(eval $(call BuildPackage,$(PKG_NAME)))
