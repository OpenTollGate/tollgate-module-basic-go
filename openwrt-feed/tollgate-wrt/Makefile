# SPDX-License-Identifier: GPL-3.0-only
#
# TollGate — OpenWrt router as a Cashu payment gateway.
#
# Upstream-shaped source-built package (EXPERIMENT, draft):
# the goal of this directory is to be eventually droppable into
# openwrt/packages as net/tollgate-wrt with no debate.
#
# Modeled after:
#   - net/v2raya: Go module living in a repository SUBDIRECTORY
#     (their "service/", our "src/"), handled by PKG_BUILD_DIR pointing
#     into the extracted tree. Their codeload-tarball + TAR strip
#     pattern is adopted here too: the git-source unpack (qbee-agent's
#     route) nests the checkout one level too deep for a subdir
#     PKG_BUILD_DIR, while the tarball + --strip-components=1 lands
#     the repo contents exactly where PKG_BUILD_DIR expects them.
#   - net/croc: version stamped via GO_PKG_LDFLAGS_X at
#     $(GO_PKG)/src/cli.Version — the same var/package shape we have.
#   - admin/qbee-agent: source pinned by full commit SHA
#     (PKG_SOURCE_VERSION) with $(GO_PKG)/app.Commit stamping —
#     the pattern for our cli.GitCommit.

include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-wrt
# APK grammar allows '-' only as -r<digits>; pre-release tags use '_'
# (the version normalization from the distribution notes). A real
# upstream PR ships only stable versions, where this never arises.
PKG_VERSION:=0.6.0_alpha2
PKG_RELEASE:=1

PKG_SOURCE:=tollgate-wrt-$(PKG_VERSION).tar.gz
PKG_SOURCE_URL:=https://codeload.github.com/OpenTollGate/tollgate-module-basic-go/tar.gz/1de228f572751aa14fd793e354d8dd66dce1490f?
PKG_HASH:=41d7ac40b79978e72aa231d9c83021981a3bf353fa6dabf6ac7fb84f098563cd
# PKG_SOURCE_VERSION is not consumed by the tarball download machinery;
# it stays as the pinned source commit for the cli.GitCommit ldflag stamp
# (qbee-agent's PKG_SOURCE_VERSION stamping pattern).
PKG_SOURCE_VERSION:=1de228f572751aa14fd793e354d8dd66dce1490f

PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)-$(PKG_VERSION)/src

PKG_MAINTAINER:=TollGate <tollgate@tollgate.me>
PKG_LICENSE:=GPL-3.0-only
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=github.com/OpenTollGate/tollgate-module-basic-go/src
GO_PKG_BUILD_PKG:= \
	$(GO_PKG)/cmd/tollgate-wrt \
	$(GO_PKG)/cmd/tollgate-cli
GO_PKG_LDFLAGS_X:= \
	$(GO_PKG)/cli.Version=v$(PKG_VERSION) \
	$(GO_PKG)/cli.GitCommit=$(PKG_SOURCE_VERSION) \
	$(GO_PKG)/cmd/tollgate-cli.version=v$(PKG_VERSION)

include $(INCLUDE_DIR)/package.mk
include ../../lang/golang/golang-package.mk

# v2raya's extraction overrides for a subdir module: strip the archive's
# top-level directory and unpack relative to PKG_BUILD_DIR's parent, so
# the repo's src/ lands exactly at PKG_BUILD_DIR.
TAR_OPTIONS+= --strip-components 1
TAR_CMD=$(HOST_TAR) -C $(1)/.. $(TAR_OPTIONS)

define Package/$(PKG_NAME)
  TITLE:=TollGate Cashu payment gateway
  SECTION:=net
  CATEGORY:=Network
  SUBMENU:=Routing and Redirection
  URL:=https://tollgate.me
  DEPENDS:=$(GO_ARCH_DEPENDS) +ca-bundle +jq +nodogsplash
endef

define Package/$(PKG_NAME)/description
  Turns an OpenWrt router into a Cashu-powered payment gateway for
  internet access: customers pay in sats (time- or data-based), the
  router gates network access via NoDogSplash and sweeps balances to
  Lightning addresses. Also buys access from upstream TollGates
  (reseller mode).
endef

define Package/$(PKG_NAME)/conffiles
/etc/tollgate/
/etc/config/tollgate
endef

define Package/$(PKG_NAME)/install
	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/tollgate-wrt $(1)/usr/bin/
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/tollgate-cli $(1)/usr/bin/tollgate

	$(INSTALL_DIR) $(1)/etc/init.d
	$(INSTALL_BIN) $(CURDIR)/files/etc/init.d/tollgate-wrt $(1)/etc/init.d/

	$(INSTALL_DIR) $(1)/etc/uci-defaults
	$(INSTALL_BIN) $(CURDIR)/files/etc/uci-defaults/90-tollgate-captive-portal-symlink $(1)/etc/uci-defaults/
	$(INSTALL_BIN) $(CURDIR)/files/etc/uci-defaults/99-tollgate-setup $(1)/etc/uci-defaults/

	$(INSTALL_DIR) $(1)/etc/tollgate/ecash
	$(INSTALL_DIR) $(1)/etc/tollgate/tollgate-captive-portal-site
	$(CP) $(CURDIR)/files/tollgate-captive-portal-site/* $(1)/etc/tollgate/tollgate-captive-portal-site/

	$(INSTALL_DIR) $(1)/usr/bin
	$(INSTALL_BIN) $(CURDIR)/files/usr/bin/check_package_path $(1)/usr/bin/
	$(INSTALL_BIN) $(CURDIR)/files/usr/bin/tollgate-apply-ssl $(1)/usr/bin/
	$(INSTALL_BIN) $(CURDIR)/files/usr/bin/tollgate-remove-ssl $(1)/usr/bin/

	$(INSTALL_DIR) $(1)/lib/upgrade/keep.d
	$(INSTALL_DATA) $(CURDIR)/files/lib/upgrade/keep.d/tollgate $(1)/lib/upgrade/keep.d/

	$(INSTALL_DIR) $(1)/etc/hotplug.d/iface
	$(INSTALL_BIN) $(CURDIR)/files/etc/hotplug.d/iface/95-tollgate-restart $(1)/etc/hotplug.d/iface/

	$(INSTALL_DIR) $(1)/etc/nftables.d
	$(INSTALL_DATA) $(sort $(wildcard $(CURDIR)/files/etc/nftables.d/*.nft)) $(1)/etc/nftables.d/

	$(INSTALL_DIR) $(1)/usr/share/man/man8
	$(INSTALL_DATA) $(CURDIR)/files/man/man8/*.8 $(1)/usr/share/man/man8/
endef

$(eval $(call BuildPackage,$(PKG_NAME)))
