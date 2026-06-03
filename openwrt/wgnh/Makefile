include $(TOPDIR)/rules.mk

PKG_NAME:=wgnh
PKG_VERSION:=0.1.3
PKG_RELEASE:=1
PKG_MAINTAINER:=wgnh maintainers
PKG_LICENSE:=MIT

PKG_BUILD_DEPENDS:=golang/host
PKG_BUILD_PARALLEL:=1
PKG_BUILD_FLAGS:=no-mips16

GO_PKG:=github.com/yfy/wireguard-natter-helper
GO_PKG_BUILD_PKG:=github.com/yfy/wireguard-natter-helper/cmd/wgnh

include $(INCLUDE_DIR)/package.mk
include $(TOPDIR)/feeds/packages/lang/golang/golang-package.mk

define Package/wgnh
  SECTION:=net
  CATEGORY:=Network
  TITLE:=WireGuard Natter Helper
  URL:=https://github.com/yfy/wireguard-natter-helper
  DEPENDS:=$(GO_ARCH_DEPENDS)
endef

define Package/wgnh/description
  WireGuard Natter Helper keeps WireGuard clients pointed at a server whose
  public UDP endpoint is created by NAT traversal and may change over time.
endef

define Package/wgnh/conffiles
/etc/wgnh/node-state.json
/etc/wgnh/state.json
/etc/wgnh/admin-token
endef

define Build/Prepare
	mkdir -p $(PKG_BUILD_DIR)
	$(CP) $(CURDIR)/../../go.mod $(PKG_BUILD_DIR)/
	$(CP) $(CURDIR)/../../cmd $(PKG_BUILD_DIR)/
	$(CP) $(CURDIR)/../../internal $(PKG_BUILD_DIR)/
endef

$(eval $(call GoBinPackage,wgnh))
$(eval $(call BuildPackage,wgnh))
