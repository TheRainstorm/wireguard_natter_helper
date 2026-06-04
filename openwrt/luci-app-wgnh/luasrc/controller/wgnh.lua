module("luci.controller.wgnh", package.seeall)

local http = require "luci.http"
local json = require "luci.jsonc"
local fs = require "nixio.fs"
local uci = require "luci.model.uci".cursor()

local function shellquote(value)
	value = tostring(value or "")
	return "'" .. value:gsub("'", "'\\''") .. "'"
end

local function daemon_config()
	local binary = uci:get("wgnh", "daemon", "binary") or "/usr/bin/wgnh"
	local addr = uci:get("wgnh", "daemon", "connect_addr") or "127.0.0.1:3333"
	local token = uci:get("wgnh", "daemon", "admin_token") or ""
	local token_file = uci:get("wgnh", "daemon", "admin_token_file") or "/etc/wgnh/admin-token"
	if token == "" and fs.access(token_file) then
		token = (fs.readfile(token_file) or ""):match("^%s*(.-)%s*$") or ""
	end
	return binary, addr, token
end

local function command_output(command)
	local fh = io.popen(command .. " 2>&1")
	if not fh then
		return nil, "failed to start command"
	end
	local raw = fh:read("*a") or ""
	local ok, reason, code = fh:close()
	if ok == nil then
		return nil, string.format("command failed: %s %s: %s", tostring(reason), tostring(code), raw)
	end
	return raw, nil
end

local function read_command(command)
	local raw, err = command_output(command)
	if not raw then
		return nil, err
	end
	local data = json.parse(raw)
	if data then
		return data, nil
	end
	return nil, raw
end

local function admin_call(subcommand, extra)
	local binary, addr, token = daemon_config()
	local command = string.format(
		"%s daemon %s --addr=%s --admin-token=%s%s",
		shellquote(binary),
		subcommand,
		shellquote(addr),
		shellquote(token),
		extra or ""
	)
	return read_command(command)
end

local function write_json(data)
	http.prepare_content("application/json")
	http.write(json.stringify(data))
end

local function event_is_error(event)
	return event and event.severity == "error"
end

local function binding_has_endpoint(binding)
	return binding and binding.endpoint_host and binding.endpoint_host ~= "" and tonumber(binding.endpoint_port or 0) > 0
end

local function build_stats(domains, nodes, bindings, events, wireguard)
	local stats = {
		domains = #domains,
		nodes = #nodes,
		online = 0,
		pending = 0,
		bindings = #bindings,
		interfaces = #wireguard,
		with_endpoint = 0,
		errors = 0
	}
	for _, node in ipairs(nodes) do
		if node.status == "online" then
			stats.online = stats.online + 1
		end
		if node.approved == false or node.status == "pending" then
			stats.pending = stats.pending + 1
		end
	end
	for _, binding in ipairs(bindings) do
		if binding_has_endpoint(binding) then
			stats.with_endpoint = stats.with_endpoint + 1
		end
	end
	for _, event in ipairs(events) do
		if event_is_error(event) then
			stats.errors = stats.errors + 1
		end
	end
	return stats
end

function index()
	if not fs.access("/etc/config/wgnh") then
		return
	end

	entry({"admin", "vpn"}, firstchild(), _("VPN"), 45).dependent = false
	entry({"admin", "vpn", "wgnh"}, firstchild(), _("WG Natter"), 60).dependent = false
	entry({"admin", "vpn", "wgnh", "status"}, template("wgnh/status"), _("Status"), 10).leaf = true
	entry({"admin", "vpn", "wgnh", "settings"}, cbi("wgnh/settings"), _("Settings"), 20).leaf = true
	entry({"admin", "vpn", "wgnh", "api", "summary"}, call("api_summary")).leaf = true
	entry({"admin", "vpn", "wgnh", "api", "local"}, call("api_local")).leaf = true
	entry({"admin", "vpn", "wgnh", "api", "create_domain"}, call("api_create_domain")).leaf = true
	entry({"admin", "vpn", "wgnh", "api", "approve_node"}, call("api_approve_node")).leaf = true
	entry({"admin", "vpn", "wgnh", "api", "delete_node"}, call("api_delete_node")).leaf = true
	entry({"admin", "vpn", "wgnh", "api", "run_natter"}, call("api_run_natter")).leaf = true
end

function api_summary()
	local domains_resp, domains_err = admin_call("domains")
	if not domains_resp then
		write_json({ ok = false, error = "load domains: " .. domains_err })
		return
	end
	local nodes_resp, nodes_err = admin_call("nodes")
	if not nodes_resp then
		write_json({ ok = false, error = "load nodes: " .. nodes_err })
		return
	end
	local members_resp, members_err = admin_call("domain-members")
	if not members_resp then
		write_json({ ok = false, error = "load domain members: " .. members_err })
		return
	end
	local bindings_resp, bindings_err = admin_call("bindings")
	if not bindings_resp then
		write_json({ ok = false, error = "load bindings: " .. bindings_err })
		return
	end
	local wg_resp, wg_err = admin_call("wireguard")
	if not wg_resp then
		write_json({ ok = false, error = "load wireguard inventory: " .. wg_err })
		return
	end
	local events_resp, events_err = admin_call("events", " --limit=80")
	if not events_resp then
		write_json({ ok = false, error = "load events: " .. events_err })
		return
	end

	local _, addr = daemon_config()
	local domains = domains_resp.domains or {}
	local nodes = nodes_resp.nodes or {}
	local members = members_resp.domain_members or {}
	local bindings = bindings_resp.bindings or {}
	local wireguard = wg_resp.wireguard_interfaces or {}
	local events = events_resp.events or {}

	write_json({
		ok = true,
		data = {
			daemon_addr = addr,
			generated_at = os.date("%Y-%m-%d %H:%M:%S"),
			domains = domains,
			nodes = nodes,
			domain_members = members,
			bindings = bindings,
			wireguard_interfaces = wireguard,
			events = events,
			stats = build_stats(domains, nodes, bindings, events, wireguard)
		}
	})
end

function api_local()
	local agent_status = command_output("/etc/init.d/wgnh-agent status")
	local daemon_status = command_output("/etc/init.d/wgnh-daemon status")
	local agent_enabled = command_output("/etc/init.d/wgnh-agent enabled")
	local daemon_enabled = command_output("/etc/init.d/wgnh-daemon enabled")
	local logs = command_output("logread 2>/dev/null | grep -i wgnh | tail -n 120")

	write_json({
		ok = true,
		data = {
			agent = {
				enabled = agent_enabled ~= nil,
				status = agent_status or "not running"
			},
			daemon = {
				enabled = daemon_enabled ~= nil,
				status = daemon_status or "not running"
			},
			logs = logs or ""
		}
	})
end

function api_create_domain()
	local domain_id = http.formvalue("domain_id")
	if not domain_id or domain_id == "" then
		write_json({ ok = false, error = "domain_id is required" })
		return
	end
	local extra = string.format(
		" --id=%s --name=%s --join-code=%s --description=%s",
		shellquote(domain_id),
		shellquote(http.formvalue("name") or ""),
		shellquote(http.formvalue("join_code") or ""),
		shellquote(http.formvalue("description") or "")
	)
	local resp, err = admin_call("create-domain", extra)
	if not resp then
		write_json({ ok = false, error = err })
		return
	end
	write_json(resp)
end

function api_approve_node()
	local node_id = http.formvalue("node_id")
	if not node_id or node_id == "" then
		write_json({ ok = false, error = "node_id is required" })
		return
	end
	local node_type = http.formvalue("node_type") or "openwrt"
	local natter_command = http.formvalue("natter_command") or ""
	local natter_timeout = http.formvalue("natter_timeout_seconds") or "0"
	local natter_delay = http.formvalue("natter_restart_delay_seconds") or "0"
	local natter_stop = http.formvalue("natter_stop_wireguard") == "1"
	local natter_control = http.formvalue("natter_wireguard_control") or ""
	if natter_control == "" then
		natter_control = node_type == "openwrt" and "ifup" or "wg-quick"
	end
	local extra = string.format(
		" --node=%s --domain=%s --role=%s --node-type=%s --interface=%s --name=%s --natter-managed --natter-command=%s --natter-timeout=%s --natter-restart-delay=%s --natter-wireguard-control=%s%s%s",
		shellquote(node_id),
		shellquote(http.formvalue("domain_id") or ""),
		shellquote(http.formvalue("role") or "client"),
		shellquote(node_type),
		shellquote(http.formvalue("interface") or "wg0"),
		shellquote(http.formvalue("name") or ""),
		shellquote(natter_command),
		shellquote(natter_timeout),
		shellquote(natter_delay),
		shellquote(natter_control),
		natter_stop and " --natter-stop-wireguard" or "",
		natter_command ~= "" and " --natter-configured" or ""
	)
	local resp, err = admin_call("approve-node", extra)
	if not resp then
		write_json({ ok = false, error = err })
		return
	end
	write_json(resp)
end

function api_delete_node()
	local node_id = http.formvalue("node_id")
	if not node_id or node_id == "" then
		write_json({ ok = false, error = "node_id is required" })
		return
	end
	local resp, err = admin_call("delete-node", " --node=" .. shellquote(node_id))
	if not resp then
		write_json({ ok = false, error = err })
		return
	end
	write_json(resp)
end

function api_run_natter()
	local server_node = http.formvalue("server_node_id")
	local server_interface = http.formvalue("server_interface")
	if not server_node or server_node == "" or not server_interface or server_interface == "" then
		write_json({ ok = false, error = "server_node_id and server_interface are required" })
		return
	end
	local extra = string.format(
		" --server-node=%s --server-interface=%s",
		shellquote(server_node),
		shellquote(server_interface)
	)
	local resp, err = admin_call("run-natter", extra)
	if not resp then
		write_json({ ok = false, error = err })
		return
	end
	write_json(resp)
end
