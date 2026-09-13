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
#     into the extracted tree — no file moves needed upstream either.
#     Their codeload-tarball + TAR strip pattern is adopted here too:
#     the git-source unpack (qbee-agent's route) nests the checkout one
#     level too deep for a subdir PKG_BUILD_DIR to line up, while the
#     tarball + --strip-components=1 lands the repo contents exactly
#     where PKG_BUILD_DIR expects them.
#   - net/croc: version stamped via GO_PKG_LDFLAGS_X at
#     $(GO_PKG)/src/cli.Version — the same var/package shape we have.
#   - admin/qbee-agent: git source pinned by full commit SHA
#     (PKG_SOURCE_VERSION) with $(GO_PKG)/app.Commit=$(PKG_SOURCE_VERSION)
#     stamping — the pattern for our cli.GitCommit.
#
# Known deferred polish for the real upstream PR (documented, not hidden):
#   - the service main package sits at the MODULE ROOT (src/main.go), so
#     `go install` names that binary "src"; upstream convention is
#     cmd/<name>/ main dirs. Renaming src/main.go into src/cmd/tollgate-wrt/
#     is a one-file move we defer to keep this experiment file-move-free;
#     the install step renames the binary meanwhile.

include $(TOPDIR)/rules.mk

PKG_NAME:=tollgate-wrt
# APK grammar allows '-' only as -r<digits>; pre-release tags use '_'
# (the version normalization from the distribution notes). A real
# upstream PR ships only stable versions, where this never arises.
PKG_VERSION:=0.6.0_alpha2
PKG_RELEASE:=1

PKG_SOURCE:=tollgate-wrt-$(PKG_VERSION).tar.gz
PKG_SOURCE_URL:=https://codeload.github.com/OpenTollGate/tollgate-module-basic-go/tar.gz/97fe25e655a920f9dd53f5dae94fdd44f0752655?
PKG_HASH:=6158e97e178e364836561a56d46c375469e7f84708b33d60f160660237185aba
# PKG_SOURCE_VERSION is not consumed by the tarball download machinery;
# it stays as the pinned source commit for the cli.GitCommit ldflag stamp
# (qbee-agent's PKG_SOURCE_VERSION stamping pattern).
PKG_SOURCE_VERSION:=97fe25e655a920f9dd53f5dae94fdd44f0752655

PKG_BUILD_DIR:=$(BUILD_DIR)/$(PKG_NAME)-$(PKG_VERSION)/src

PKG_MAINTAINER:=TollGate <tollgate@tollgate.me>
PKG_LICENSE:=GPL-3.0-only
PKG_LICENSE_FILES:=LICENSE

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=github.com/OpenTollGate/tollgate-module-basic-go/src
GO_PKG_BUILD_PKG:= \
	$(GO_PKG) \
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
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/src $(1)/usr/bin/tollgate-wrt
	$(INSTALL_BIN) $(GO_PKG_BUILD_BIN_DIR)/tollgate-cli $(1)/usr/bin/tollgate

	$(CP) $(CURDIR)/files/. $(1)/
endef

$(eval $(call BuildPackage,$(PKG_NAME)))
