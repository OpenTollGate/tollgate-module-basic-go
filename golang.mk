#
# Copyright (C) 2018 OpenWrt.org
#
# This is free software, licensed under the GNU General Public License v2.
# See /LICENSE for more information.
#

ifneq ($(__golang_mk_inc),1)
__golang_mk_inc=1

ifeq ($(DUMP),)
GO_VERSION_MAJOR_MINOR:=$(shell go version | sed -E 's/.*go([0-9]+[.][0-9]+).*/\1/')
endif

GO_ARM:=$(if $(CONFIG_arm),$(if $(CONFIG_HAS_FPU),7,$(if $(CONFIG_GOARM_5),5,$(if $(CONFIG_GOARM_6),6,7))))
GO_MIPS:=$(if $(CONFIG_mips),$(if $(CONFIG_MIPS_FP_32),hardfloat,softfloat),)
GO_MIPS64:=$(if $(CONFIG_mips64),$(if $(CONFIG_MIPS_FP_64),hardfloat,softfloat),)
GO_386:=$(if $(CONFIG_i386),$(if $(CONFIG_CPU_TYPE_PENTIUM4),387,sse2),)

GO_TARGET_ARCH:=$(subst aarch64,arm64,$(subst x86_64,amd64,$(subst i386,386,$(ARCH))))
GO_TARGET_OS:=linux

GO_HOST_ARCH:=$(shell go env GOHOSTARCH)
GO_HOST_OS:=$(shell go env GOHOSTOS)

GO_HOST_TARGET_SAME:=$(if $(and $(findstring $(GO_TARGET_ARCH),$(GO_HOST_ARCH)),$(findstring $(GO_TARGET_OS),$(GO_HOST_OS))),1)
GO_HOST_TARGET_DIFFERENT:=$(if $(GO_HOST_TARGET_SAME),,1)

GO_STRIP_ARGS:=--strip-unneeded --remove-section=.comment --remove-section=.note
GO_PKG_GCFLAGS:=
GO_PKG_LDFLAGS:=-s -w

GO_PKG_BUILD_PKG?=$(GO_PKG)/...

GO_PKG_WORK_DIR_NAME:=.go_work
GO_PKG_WORK_DIR:=$(PKG_BUILD_DIR)/$(GO_PKG_WORK_DIR_NAME)

GO_PKG_BUILD_DIR:=$(GO_PKG_WORK_DIR)/build
GO_PKG_CACHE_DIR:=$(GO_PKG_WORK_DIR)/cache
GO_PKG_TMP_DIR:=$(GO_PKG_WORK_DIR)/tmp

GO_PKG_BUILD_BIN_DIR:=$(GO_PKG_BUILD_DIR)/bin$(if $(GO_HOST_TARGET_DIFFERENT),/$(GO_TARGET_OS)_$(GO_TARGET_ARCH))

GO_BUILD_DIR_PATH:=$(firstword $(subst :, ,$(GOPATH)))
GO_BUILD_PATH:=$(if $(GO_PKG),$(GO_BUILD_DIR_PATH)/src/$(GO_PKG))

endif # __golang_mk_inc
