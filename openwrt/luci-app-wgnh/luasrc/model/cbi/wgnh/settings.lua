local m, s, o

m = Map("wgnh", translate("WireGuard Natter Helper"),
	translate("Configure the daemon connection used by the LuCI status page and the local OpenWrt services."))

s = m:section(NamedSection, "daemon", "daemon", translate("Daemon connection and local daemon service"))
s.anonymous = false

o = s:option(Value, "connect_addr", translate("Daemon address used by LuCI status"))
o.description = translate("For example: ecs01.yfycloud.site:3333. This is the daemon that Status reads with wgnh daemon nodes/bindings/events.")
o.default = "127.0.0.1:3333"
o.rmempty = false

o = s:option(Value, "admin_token", translate("Admin token used by LuCI status"))
o.description = translate("Same value as wgnh daemon serve --admin-token. Leave empty to read the first line from the admin token file.")
o.password = true
o.rmempty = true

o = s:option(Value, "admin_token_file", translate("Admin token file"))
o.description = translate("Used when Admin token is empty.")
o.default = "/etc/wgnh/admin-token"
o.rmempty = true

o = s:option(Flag, "enabled", translate("Enable local daemon service"))
o.description = translate("Enable this only when this router itself should run wgnh daemon.")
o.rmempty = false

o = s:option(Value, "state", translate("State file"))
o.default = "/etc/wgnh/state.json"
o.rmempty = false

o = s:option(Value, "listen_addr", translate("Listen address"))
o.default = "0.0.0.0:3333"
o.rmempty = false

o = s:option(Value, "binary", translate("Binary path"))
o.default = "/usr/bin/wgnh"
o.rmempty = false

o = s:option(Value, "natter_cooldown", translate("Natter cooldown"))
o.default = "5m"
o.rmempty = false

s = m:section(NamedSection, "agent", "agent", translate("Local agent service"))
s.anonymous = false

o = s:option(Flag, "enabled", translate("Enable local agent service"))
o.description = translate("Enable this when this router should run wgnh agent, for example as a WireGuard client or NAT-side server node.")
o.rmempty = false

o = s:option(Value, "binary", translate("Binary path"))
o.default = "/usr/bin/wgnh"
o.rmempty = false

o = s:option(Value, "daemon_addr", translate("Daemon address"))
o.description = translate("The only required agent setting. Leave empty to reuse the daemon address configured above.")
o.placeholder = "ecs01.yfycloud.site:3333"
o.rmempty = true

o = s:option(Value, "state_path", translate("Node state file"))
o.description = translate("Stores the generated node id and token. Keep this file when upgrading or rebooting.")
o.default = "/etc/wgnh/node-state.json"
o.rmempty = false

o = s:option(Value, "retry_seconds", translate("Retry interval"))
o.datatype = "uinteger"
o.default = "5"
o.rmempty = false

o = s:option(Flag, "dry_run", translate("Dry run"))
o.description = translate("Report actions without changing WireGuard configuration.")
o.rmempty = false

return m
