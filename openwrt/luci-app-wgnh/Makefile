include $(TOPDIR)/rules.mk

PKG_NAME:=luci-app-wgnh
PKG_VERSION:=0.1.0
PKG_RELEASE:=1
PKG_MAINTAINER:=wgnh maintainers
PKG_LICENSE:=MIT

LUCI_TITLE:=LuCI support for WireGuard Natter Helper
LUCI_DEPENDS:=+luci-base +luci-compat +wgnh
LUCI_PKGARCH:=all

include $(TOPDIR)/feeds/luci/luci.mk

# call BuildPackage - OpenWrt buildroot signature
