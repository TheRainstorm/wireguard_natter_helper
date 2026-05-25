local m, s, o

m = Map("wgnh", translate("WireGuard Natter Helper"),
	translate("Configure wgnh services used by the LuCI dashboard. The wgnh binary itself must be installed on this router."))

s = m:section(NamedSection, "daemon", "daemon", translate("Daemon"))
s.anonymous = false

o = s:option(Flag, "enabled", translate("Enable daemon service"))
o.rmempty = false

o = s:option(Value, "binary", translate("Binary path"))
o.default = "/usr/bin/wgnh"
o.rmempty = false

o = s:option(Value, "state", translate("State file"))
o.default = "/etc/wgnh/state.json"
o.rmempty = false

o = s:option(Value, "listen_addr", translate("Listen address"))
o.default = "0.0.0.0:3333"
o.rmempty = false

o = s:option(Value, "connect_addr", translate("Dashboard daemon address"))
o.default = "127.0.0.1:3333"
o.rmempty = false

o = s:option(Value, "admin_token", translate("Admin token"))
o.password = true
o.rmempty = true

o = s:option(Value, "admin_token_file", translate("Admin token file"))
o.default = "/etc/wgnh/admin-token"
o.rmempty = true

o = s:option(Value, "natter_cooldown", translate("Natter cooldown"))
o.default = "5m"
o.rmempty = false

s = m:section(NamedSection, "agent", "agent", translate("Agent"))
s.anonymous = false

o = s:option(Flag, "enabled", translate("Enable agent service"))
o.rmempty = false

o = s:option(Value, "binary", translate("Binary path"))
o.default = "/usr/bin/wgnh"
o.rmempty = false

o = s:option(Value, "config_path", translate("Agent config"))
o.default = "/etc/wgnh/agent.json"
o.rmempty = false

s = m:section(NamedSection, "web", "web", translate("Standalone Web UI"))
s.anonymous = false

o = s:option(Flag, "enabled", translate("Enable standalone web service"))
o.rmempty = false

o = s:option(Value, "binary", translate("Binary path"))
o.default = "/usr/bin/wgnh"
o.rmempty = false

o = s:option(Value, "listen_addr", translate("Listen address"))
o.default = "0.0.0.0:9090"
o.rmempty = false

o = s:option(Value, "daemon_addr", translate("Default daemon address"))
o.default = "127.0.0.1:3333"
o.rmempty = false

return m
