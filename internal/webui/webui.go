package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yfy/wireguard-natter-helper/internal/rpc"
	"github.com/yfy/wireguard-natter-helper/internal/store"
)

type Server struct {
	addr       string
	daemonAddr string
	adminToken string
}

type dashboardData struct {
	DaemonAddr   string              `json:"daemon_addr"`
	GeneratedAt  string              `json:"generated_at"`
	Domains      []store.Domain      `json:"domains"`
	Nodes        []store.Node        `json:"nodes"`
	WGInterfaces []store.WGInterface `json:"wireguard_interfaces"`
	Bindings     []store.Binding     `json:"bindings"`
	Events       []store.Event       `json:"events"`
	Stats        stats               `json:"stats"`
}

type stats struct {
	Nodes        int `json:"nodes"`
	Online       int `json:"online"`
	Pending      int `json:"pending"`
	Domains      int `json:"domains"`
	Bindings     int `json:"bindings"`
	Interfaces   int `json:"interfaces"`
	WithEndpoint int `json:"with_endpoint"`
	Errors       int `json:"errors"`
}

type daemonCredentials struct {
	DaemonAddr string `json:"daemon_addr"`
	AdminToken string `json:"admin_token"`
}

func New(addr, daemonAddr, adminToken string) *Server {
	return &Server{addr: addr, daemonAddr: daemonAddr, adminToken: adminToken}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/summary", s.apiSummary)
	mux.HandleFunc("/api/create-domain", s.apiCreateDomain)
	mux.HandleFunc("/api/approve-node", s.apiApproveNode)
	mux.HandleFunc("/api/run-natter", s.apiRunNatter)
	log.Printf("wgnh web ui listening on http://%s", s.addr)
	return http.ListenAndServe(s.addr, mux)
}

func (s *Server) apiCreateDomain(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	var req struct {
		daemonCredentials
		DomainID    string `json:"domain_id"`
		Name        string `json:"name"`
		JoinCode    string `json:"join_code"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	creds := s.withDefaults(req.daemonCredentials)
	if err := validateCredentials(creds); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.DomainID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "domain_id is required"})
		return
	}
	resp, err := rpc.Call(r.Context(), creds.DaemonAddr, rpc.Request{
		Kind:        "admin.create_domain",
		AdminToken:  creds.AdminToken,
		DomainID:    strings.TrimSpace(req.DomainID),
		Name:        strings.TrimSpace(req.Name),
		JoinCode:    strings.TrimSpace(req.JoinCode),
		Description: strings.TrimSpace(req.Description),
	}, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) apiApproveNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	var req struct {
		daemonCredentials
		NodeID                    string   `json:"node_id"`
		DomainID                  string   `json:"domain_id"`
		Role                      string   `json:"role"`
		NodeType                  string   `json:"node_type"`
		Interface                 string   `json:"interface"`
		ConfigType                string   `json:"config_type"`
		ReloadMethod              string   `json:"reload_method"`
		NatterManaged             bool     `json:"natter_managed"`
		NatterCommand             []string `json:"natter_command"`
		NatterConfigured          bool     `json:"natter_configured"`
		NatterTimeoutSeconds      int      `json:"natter_timeout_seconds"`
		NatterStopWireGuard       bool     `json:"natter_stop_wireguard"`
		NatterWireGuardControl    string   `json:"natter_wireguard_control"`
		NatterRestartDelaySeconds int      `json:"natter_restart_delay_seconds"`
		Name                      string   `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	creds := s.withDefaults(req.daemonCredentials)
	if err := validateCredentials(creds); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if strings.TrimSpace(req.NodeID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "node_id is required"})
		return
	}
	resp, err := rpc.Call(r.Context(), creds.DaemonAddr, rpc.Request{
		Kind:                      "admin.approve_node",
		AdminToken:                creds.AdminToken,
		NodeID:                    strings.TrimSpace(req.NodeID),
		DomainID:                  strings.TrimSpace(req.DomainID),
		Role:                      strings.TrimSpace(req.Role),
		NodeType:                  strings.TrimSpace(req.NodeType),
		Interface:                 strings.TrimSpace(req.Interface),
		ConfigType:                strings.TrimSpace(req.ConfigType),
		ReloadMethod:              strings.TrimSpace(req.ReloadMethod),
		NatterManaged:             req.NatterManaged,
		NatterConfigured:          req.NatterConfigured,
		NatterCommand:             req.NatterCommand,
		NatterTimeoutSeconds:      req.NatterTimeoutSeconds,
		NatterStopWireGuard:       req.NatterStopWireGuard,
		NatterWireGuardControl:    strings.TrimSpace(req.NatterWireGuardControl),
		NatterRestartDelaySeconds: req.NatterRestartDelaySeconds,
		Name:                      strings.TrimSpace(req.Name),
	}, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, map[string]string{
		"DefaultDaemonAddr": s.daemonAddr,
	}); err != nil {
		log.Printf("render web ui failed: %v", err)
	}
}

func (s *Server) apiSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	var req daemonCredentials
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req = s.withDefaults(req)
	if err := validateCredentials(req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	data, err := load(r.Context(), req.DaemonAddr, req.AdminToken)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func (s *Server) apiRunNatter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"ok": false, "error": "method not allowed"})
		return
	}
	var req struct {
		daemonCredentials
		ServerNodeID    string `json:"server_node_id"`
		ServerInterface string `json:"server_interface"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	creds := s.withDefaults(req.daemonCredentials)
	if err := validateCredentials(creds); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.ServerNodeID == "" || req.ServerInterface == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "server_node_id and server_interface are required"})
		return
	}
	resp, err := rpc.Call(r.Context(), creds.DaemonAddr, rpc.Request{
		Kind:            "admin.run_natter",
		AdminToken:      creds.AdminToken,
		ServerNodeID:    req.ServerNodeID,
		ServerInterface: req.ServerInterface,
	}, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) withDefaults(req daemonCredentials) daemonCredentials {
	req.DaemonAddr = strings.TrimSpace(req.DaemonAddr)
	if req.DaemonAddr == "" {
		req.DaemonAddr = s.daemonAddr
	}
	if req.AdminToken == "" {
		req.AdminToken = s.adminToken
	}
	return req
}

func validateCredentials(req daemonCredentials) error {
	if strings.TrimSpace(req.DaemonAddr) == "" {
		return fmt.Errorf("daemon address is required")
	}
	return nil
}

func load(ctx context.Context, daemonAddr, adminToken string) (dashboardData, error) {
	domainsResp, err := rpc.Call(ctx, daemonAddr, rpc.Request{Kind: "admin.domains", AdminToken: adminToken}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load domains: %w", err)
	}
	nodesResp, err := rpc.Call(ctx, daemonAddr, rpc.Request{Kind: "admin.nodes", AdminToken: adminToken}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load nodes: %w", err)
	}
	bindingsResp, err := rpc.Call(ctx, daemonAddr, rpc.Request{Kind: "admin.bindings", AdminToken: adminToken}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load bindings: %w", err)
	}
	wgResp, err := rpc.Call(ctx, daemonAddr, rpc.Request{Kind: "admin.wireguard", AdminToken: adminToken}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load wireguard inventory: %w", err)
	}
	eventsResp, err := rpc.Call(ctx, daemonAddr, rpc.Request{Kind: "admin.events", AdminToken: adminToken, Limit: 80}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load events: %w", err)
	}

	domains := domainsResp.Domains
	nodes := nodesResp.Nodes
	bindings := bindingsResp.Bindings
	wgInterfaces := wgResp.WGInterfaces
	events := eventsResp.Events
	if domains == nil {
		domains = []store.Domain{}
	}
	if nodes == nil {
		nodes = []store.Node{}
	}
	if bindings == nil {
		bindings = []store.Binding{}
	}
	if wgInterfaces == nil {
		wgInterfaces = []store.WGInterface{}
	}
	if events == nil {
		events = []store.Event{}
	}
	sort.Slice(domains, func(i, j int) bool { return domains[i].ID < domains[j].ID })
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	sort.Slice(wgInterfaces, func(i, j int) bool {
		if wgInterfaces[i].NodeID == wgInterfaces[j].NodeID {
			return wgInterfaces[i].Name < wgInterfaces[j].Name
		}
		return wgInterfaces[i].NodeID < wgInterfaces[j].NodeID
	})

	st := stats{Nodes: len(nodes), Domains: len(domains), Bindings: len(bindings), Interfaces: len(wgInterfaces)}
	for _, node := range nodes {
		if node.Status == "online" {
			st.Online++
		}
		if !node.Approved || node.Status == "pending" {
			st.Pending++
		}
	}
	for _, binding := range bindings {
		if binding.EndpointHost != "" && binding.EndpointPort > 0 {
			st.WithEndpoint++
		}
	}
	for _, event := range events {
		if event.Severity == "error" {
			st.Errors++
		}
	}

	return dashboardData{
		DaemonAddr:   daemonAddr,
		GeneratedAt:  time.Now().Format("2006-01-02 15:04:05"),
		Domains:      domains,
		Nodes:        nodes,
		WGInterfaces: wgInterfaces,
		Bindings:     bindings,
		Events:       events,
		Stats:        st,
	}, nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		status = http.StatusInternalServerError
		raw = []byte(`{"ok":false,"error":"json encode failed"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

var pageTemplate = template.Must(template.New("index").Parse(pageHTML))

const pageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>WireGuard Natter Helper</title>
  <style>
    :root {
      --bg: #f6f7f9;
      --panel: #ffffff;
      --panel-2: #eef3f8;
      --text: #1d2430;
      --muted: #667085;
      --line: #d9e0e7;
      --accent: #0f766e;
      --accent-2: #155e75;
      --danger: #b42318;
      --warn: #b54708;
      --ok: #027a48;
      --shadow: 0 10px 28px rgba(30, 41, 59, .08);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font: 14px/1.45 system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      color: var(--text);
      background: var(--bg);
    }
    header {
      background: #ffffff;
      border-bottom: 1px solid var(--line);
      padding: 18px 24px;
      position: sticky;
      top: 0;
      z-index: 10;
    }
    .top {
      max-width: 1280px;
      margin: 0 auto;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 16px;
    }
    h1 { margin: 0; font-size: 20px; letter-spacing: 0; }
    .sub { color: var(--muted); margin-top: 2px; }
    main { max-width: 1280px; margin: 0 auto; padding: 24px; }
    .connect {
      display: grid;
      grid-template-columns: minmax(220px, 1.2fr) minmax(220px, 1fr) auto auto auto;
      gap: 10px;
      align-items: end;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 14px;
      margin-bottom: 18px;
      box-shadow: var(--shadow);
    }
    label { display: grid; gap: 5px; color: var(--muted); font-size: 12px; font-weight: 650; }
    input, select, textarea {
      width: 100%;
      min-height: 38px;
      border: 1px solid var(--line);
      border-radius: 7px;
      padding: 8px 10px;
      color: var(--text);
      background: #fff;
      font: inherit;
    }
    input[type="checkbox"] { width: auto; min-height: auto; }
    textarea { min-height: 72px; resize: vertical; }
    .stats {
      display: grid;
      grid-template-columns: repeat(7, minmax(120px, 1fr));
      gap: 12px;
      margin-bottom: 18px;
    }
    .stat {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 16px;
      box-shadow: var(--shadow);
    }
    .stat .label { color: var(--muted); font-size: 12px; }
    .stat .value { font-size: 26px; font-weight: 700; margin-top: 4px; }
    section {
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 8px;
      margin-top: 16px;
      box-shadow: var(--shadow);
      overflow: hidden;
    }
    .section-head {
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 16px;
      padding: 14px 16px;
      border-bottom: 1px solid var(--line);
      background: var(--panel-2);
    }
    h2 { margin: 0; font-size: 15px; }
    table { width: 100%; border-collapse: collapse; }
    th, td {
      text-align: left;
      padding: 10px 12px;
      border-bottom: 1px solid var(--line);
      vertical-align: top;
      white-space: nowrap;
    }
    th { color: var(--muted); font-weight: 600; font-size: 12px; background: #fbfcfd; }
    tr:last-child td { border-bottom: 0; }
    code {
      font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
      background: #f2f4f7;
      border: 1px solid #e4e7ec;
      border-radius: 6px;
      padding: 2px 5px;
    }
    .badge {
      display: inline-flex;
      align-items: center;
      min-height: 22px;
      padding: 2px 8px;
      border-radius: 999px;
      font-size: 12px;
      border: 1px solid var(--line);
      color: var(--muted);
      background: #fff;
    }
    .online { color: var(--ok); border-color: #abefc6; background: #ecfdf3; }
    .offline { color: var(--danger); border-color: #fecdca; background: #fef3f2; }
    .warning { color: var(--warn); }
    .error { color: var(--danger); }
    button {
      appearance: none;
      border: 0;
      border-radius: 7px;
      background: var(--accent);
      color: white;
      min-height: 38px;
      padding: 8px 11px;
      font-weight: 650;
      cursor: pointer;
    }
    button.secondary { background: var(--accent-2); }
    button.ghost {
      color: var(--accent-2);
      background: #e6f4f1;
      border: 1px solid #b7dfd8;
    }
    button:disabled { opacity: .55; cursor: wait; }
    .toolbar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
    .form-grid {
      display: grid;
      grid-template-columns: 180px minmax(180px, 1fr) minmax(220px, 1.4fr) auto;
      gap: 10px;
      align-items: end;
      padding: 14px 16px;
      border-bottom: 1px solid var(--line);
      background: #fbfcfd;
    }
    .domain-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
      gap: 12px;
      padding: 14px 16px;
    }
    .domain {
      border: 1px solid var(--line);
      border-radius: 8px;
      padding: 12px;
      background: #fff;
    }
    .domain-title { display: flex; justify-content: space-between; gap: 10px; align-items: flex-start; }
    .domain-title strong { font-size: 14px; }
    .domain-desc { margin-top: 8px; color: var(--muted); white-space: normal; }
    .join-result {
      display: none;
      margin: 12px 16px 0;
      padding: 10px 12px;
      border: 1px solid #abefc6;
      border-radius: 8px;
      background: #ecfdf3;
      color: #067647;
    }
    .approval {
      display: grid;
      grid-template-columns: repeat(7, minmax(120px, 1fr)) auto;
      gap: 8px;
      min-width: 980px;
    }
    .node-name { display: grid; gap: 5px; }
    .check { display: inline-flex; align-items: center; gap: 6px; min-height: 38px; color: var(--text); font-size: 13px; }
    .mini { font-size: 12px; color: var(--muted); }
    .muted { color: var(--muted); }
    .wrap { white-space: normal; min-width: 220px; }
    .scroll { overflow-x: auto; }
    .empty { padding: 20px; color: var(--muted); }
    .toast {
      position: fixed;
      right: 18px;
      bottom: 18px;
      max-width: 460px;
      background: #111827;
      color: white;
      padding: 12px 14px;
      border-radius: 8px;
      box-shadow: var(--shadow);
      display: none;
      z-index: 20;
    }
    @media (max-width: 980px) {
      .top { align-items: flex-start; flex-direction: column; }
      .connect { grid-template-columns: 1fr; }
      .form-grid { grid-template-columns: 1fr; }
      .stats { grid-template-columns: repeat(2, minmax(130px, 1fr)); }
      main { padding: 16px; }
      button { width: 100%; }
    }
  </style>
</head>
<body>
  <header>
    <div class="top">
      <div>
        <h1>WireGuard Natter Helper</h1>
        <div id="subtitle" class="sub">输入 daemon 地址和 admin token 后连接，浏览器会保存配置。</div>
      </div>
      <div class="toolbar">
        <button class="secondary" id="refreshBtn">刷新</button>
        <button class="ghost" id="pauseRefreshBtn">暂停自动刷新</button>
      </div>
    </div>
  </header>
  <main>
    <div class="connect">
      <label>Daemon TCP 地址
        <input id="daemonAddr" autocomplete="off" placeholder="your-vps.example.com:3333" value="{{.DefaultDaemonAddr}}">
      </label>
      <label>Admin Token
        <input id="adminToken" type="password" autocomplete="current-password" placeholder="输入 daemon admin token">
      </label>
      <button id="connectBtn">连接并保存</button>
      <button class="ghost" id="forgetBtn">忘记</button>
      <button class="secondary" id="showTokenBtn">显示 Token</button>
    </div>

    <div class="stats">
      <div class="stat"><div class="label">Domain</div><div id="statDomains" class="value">-</div></div>
      <div class="stat"><div class="label">节点</div><div id="statNodes" class="value">-</div></div>
      <div class="stat"><div class="label">在线节点</div><div id="statOnline" class="value">-</div></div>
      <div class="stat"><div class="label">待审批</div><div id="statPending" class="value">-</div></div>
      <div class="stat"><div class="label">WG 接口</div><div id="statInterfaces" class="value">-</div></div>
      <div class="stat"><div class="label">绑定</div><div id="statBindings" class="value">-</div></div>
      <div class="stat"><div class="label">错误事件</div><div id="statErrors" class="value">-</div></div>
    </div>

    <section>
      <div class="section-head">
        <h2>Domain</h2>
        <span class="muted">创建后启动只带 daemon_addr 的 agent，再在这里审批节点</span>
      </div>
      <div class="form-grid">
        <label>Domain ID
          <input id="domainID" autocomplete="off" placeholder="home">
        </label>
        <label>名称
          <input id="domainName" autocomplete="off" placeholder="家庭网络">
        </label>
        <label>说明
          <input id="domainDescription" autocomplete="off" placeholder="可选，例如 home-a server + mobile peers">
        </label>
        <button id="createDomainBtn">创建 Domain</button>
      </div>
      <div id="joinResult" class="join-result"></div>
      <div id="domainsBody" class="domain-grid"><div class="empty">尚未连接 daemon</div></div>
    </section>

    <section>
      <div class="section-head"><h2>节点</h2></div>
      <div class="scroll">
        <table>
          <thead><tr><th>节点</th><th>Domain</th><th>角色</th><th>状态</th><th>平台</th><th>接口</th><th>最后心跳</th><th>操作</th></tr></thead>
          <tbody id="nodesBody"><tr><td colspan="8" class="empty">尚未连接 daemon</td></tr></tbody>
        </table>
      </div>
    </section>

    <section>
      <div class="section-head">
        <h2>WireGuard 自动发现</h2>
        <span class="muted">server 公钥出现在 client peer 列表中时会自动生成绑定</span>
      </div>
      <div class="scroll">
        <table>
          <thead><tr><th>节点</th><th>接口</th><th>本机公钥</th><th>Listen</th><th>Peers</th><th>配置</th><th>更新时间</th></tr></thead>
          <tbody id="interfacesBody"><tr><td colspan="7" class="empty">尚未连接 daemon</td></tr></tbody>
        </table>
      </div>
    </section>

    <section>
      <div class="section-head"><h2>WireGuard 绑定</h2></div>
      <div class="scroll">
        <table>
          <thead><tr><th>绑定</th><th>Server</th><th>Client</th><th>Endpoint</th><th>配置</th><th>操作</th></tr></thead>
          <tbody id="bindingsBody"><tr><td colspan="6" class="empty">尚未连接 daemon</td></tr></tbody>
        </table>
      </div>
    </section>

    <section>
      <div class="section-head"><h2>最近事件</h2></div>
      <div class="scroll">
        <table>
          <thead><tr><th>时间</th><th>级别</th><th>类型</th><th>节点</th><th>绑定</th><th>消息</th><th>Payload</th></tr></thead>
          <tbody id="eventsBody"><tr><td colspan="7" class="empty">尚未连接 daemon</td></tr></tbody>
        </table>
      </div>
    </section>
  </main>
  <div id="toast" class="toast"></div>
  <script>
    const storage = {
      daemonAddr: 'wgnh.web.daemonAddr',
      adminToken: 'wgnh.web.adminToken'
    };
    const defaultDaemonAddr = '{{.DefaultDaemonAddr}}';
    const daemonAddrInput = document.getElementById('daemonAddr');
    const adminTokenInput = document.getElementById('adminToken');
    const connectBtn = document.getElementById('connectBtn');
    const refreshBtn = document.getElementById('refreshBtn');
    const pauseRefreshBtn = document.getElementById('pauseRefreshBtn');
    const forgetBtn = document.getElementById('forgetBtn');
    const showTokenBtn = document.getElementById('showTokenBtn');
    const createDomainBtn = document.getElementById('createDomainBtn');
    let refreshTimer = null;
    let autoRefreshPaused = false;
    let nodeFormDirty = false;

    daemonAddrInput.value = localStorage.getItem(storage.daemonAddr) || daemonAddrInput.value || defaultDaemonAddr;
    adminTokenInput.value = localStorage.getItem(storage.adminToken) || '';

    connectBtn.addEventListener('click', () => refresh(true));
    refreshBtn.addEventListener('click', () => refresh(false));
    pauseRefreshBtn.addEventListener('click', toggleAutoRefresh);
    forgetBtn.addEventListener('click', forgetConnection);
    showTokenBtn.addEventListener('click', toggleToken);
    createDomainBtn.addEventListener('click', createDomain);

    if (daemonAddrInput.value && adminTokenInput.value) {
      refresh(false);
    }

    function credentials() {
      return {
        daemon_addr: daemonAddrInput.value.trim(),
        admin_token: adminTokenInput.value
      };
    }

    function saveConnection() {
      const creds = credentials();
      localStorage.setItem(storage.daemonAddr, creds.daemon_addr);
      localStorage.setItem(storage.adminToken, creds.admin_token);
    }

    function forgetConnection() {
      localStorage.removeItem(storage.daemonAddr);
      localStorage.removeItem(storage.adminToken);
      adminTokenInput.value = '';
      daemonAddrInput.value = defaultDaemonAddr || '';
      clearRefreshTimer();
      nodeFormDirty = false;
      document.getElementById('subtitle').textContent = '已清除浏览器保存的连接信息。';
      toast('已忘记连接信息');
    }

    function toggleToken() {
      const showing = adminTokenInput.type === 'text';
      adminTokenInput.type = showing ? 'password' : 'text';
      showTokenBtn.textContent = showing ? '显示 Token' : '隐藏 Token';
    }

    async function refresh(shouldSave, options) {
      options = options || {};
      if (options.auto && (autoRefreshPaused || nodeFormDirty || isEditingNodeConfig())) {
        scheduleRefresh();
        document.getElementById('subtitle').textContent = autoRefreshPaused ? '自动刷新已暂停。' : '正在编辑节点配置，已跳过本次自动刷新。';
        return;
      }
      const creds = credentials();
      if (!creds.daemon_addr) {
        toast('请先输入 daemon TCP 地址');
        return;
      }
      if (shouldSave) {
        saveConnection();
      }
      setBusy(true);
      try {
        const data = await postJSON('/api/summary', creds);
        render(data.data);
        if (shouldSave) {
          toast('连接成功，已保存到浏览器');
        }
        scheduleRefresh();
      } catch (err) {
        clearRefreshTimer();
        toast('连接失败: ' + err.message);
        document.getElementById('subtitle').textContent = '连接失败: ' + err.message;
      } finally {
        setBusy(false);
      }
    }

    async function runNatter(serverNodeID, serverInterface, btn) {
      btn.disabled = true;
      try {
        const data = await postJSON('/api/run-natter', {
          ...credentials(),
          server_node_id: serverNodeID,
          server_interface: serverInterface
        });
        toast('已下发 natter.run: ' + (data.command && data.command.command_id ? data.command.command_id : 'queued'));
        await refresh(false);
      } catch (err) {
        toast('失败: ' + err.message);
      } finally {
        btn.disabled = false;
      }
    }

    async function createDomain() {
      const domainID = document.getElementById('domainID').value.trim();
      const name = document.getElementById('domainName').value.trim();
      const description = document.getElementById('domainDescription').value.trim();
      if (!domainID) {
        toast('请先填写 Domain ID');
        return;
      }
      createDomainBtn.disabled = true;
      try {
        const data = await postJSON('/api/create-domain', {
          ...credentials(),
          domain_id: domainID,
          name,
          description
        });
        const domain = data.domain || {};
        showJoinResult(domain);
        document.getElementById('domainID').value = '';
        document.getElementById('domainName').value = '';
        document.getElementById('domainDescription').value = '';
        toast('Domain 已创建: ' + (domain.id || domainID));
        await refresh(false);
      } catch (err) {
        toast('创建失败: ' + err.message);
      } finally {
        createDomainBtn.disabled = false;
      }
    }

    async function approveNode(node, idx, btn) {
      const prefix = 'approve-' + idx + '-';
      const role = field(prefix + 'role').value;
      const natterCommand = splitCommand(field(prefix + 'natterCommand').value);
      const payload = {
        ...credentials(),
        node_id: node.id,
        domain_id: field(prefix + 'domain').value,
        role,
        node_type: field(prefix + 'nodeType').value,
        interface: field(prefix + 'interface').value.trim(),
        config_type: field(prefix + 'configType').value,
        reload_method: field(prefix + 'reloadMethod').value,
        name: field(prefix + 'name').value.trim()
      };
      if (role === 'server') {
        payload.natter_managed = true;
        payload.natter_configured = natterCommand.length > 0;
        payload.natter_command = natterCommand;
        payload.natter_timeout_seconds = numberValue(field(prefix + 'natterTimeout').value);
        payload.natter_stop_wireguard = field(prefix + 'natterStop').checked;
        payload.natter_wireguard_control = field(prefix + 'natterControl').value;
        payload.natter_restart_delay_seconds = numberValue(field(prefix + 'natterRestartDelay').value);
      } else {
        payload.natter_managed = true;
        payload.natter_configured = false;
      }
      if (!payload.domain_id || !payload.role || !payload.interface) {
        toast('审批需要选择 domain、角色并填写接口');
        return;
      }
      btn.disabled = true;
      try {
        const data = await postJSON('/api/approve-node', payload);
        const saved = data.nodes && data.nodes.length ? data.nodes[0] : {};
        nodeFormDirty = false;
        toast((node.approved ? '节点配置已保存: ' : '节点已审批: ') + (saved.name || payload.name || node.id));
        await refresh(false);
      } catch (err) {
        toast('审批失败: ' + err.message);
      } finally {
        btn.disabled = false;
      }
    }

    async function postJSON(url, body) {
      const res = await fetch(url, {
        method: 'POST',
        headers: {'Content-Type': 'application/json'},
        body: JSON.stringify(body)
      });
      const data = await res.json();
      if (!res.ok || !data.ok) throw new Error(data.error || 'request failed');
      return data;
    }

    function render(data) {
      document.getElementById('subtitle').innerHTML = 'daemon <code>' + escapeHTML(data.daemon_addr) + '</code> · ' + escapeHTML(data.generated_at) + ' · ' + (autoRefreshPaused ? '自动刷新已暂停' : '自动刷新 15s');
      document.getElementById('statDomains').textContent = data.stats.domains;
      document.getElementById('statNodes').textContent = data.stats.nodes;
      document.getElementById('statOnline').textContent = data.stats.online;
      document.getElementById('statPending').textContent = data.stats.pending;
      document.getElementById('statInterfaces').textContent = data.stats.interfaces;
      document.getElementById('statBindings').textContent = data.stats.bindings;
      document.getElementById('statErrors').textContent = data.stats.errors;
      renderDomains(data.domains || []);
      renderNodes(data.nodes || [], data.domains || []);
      renderInterfaces(data.wireguard_interfaces || []);
      renderBindings(data.bindings || []);
      renderEvents(data.events || []);
    }

    function renderDomains(domains) {
      const body = document.getElementById('domainsBody');
      if (!domains.length) {
        body.innerHTML = '<div class="empty">暂无 domain。先创建一个 domain，然后启动只配置 daemon_addr 的 agent，节点会出现在待审批列表里。</div>';
        return;
      }
      body.innerHTML = domains.map(domain => '<div class="domain">'
        + '<div class="domain-title"><div><strong>' + escapeHTML(domain.name || domain.id) + '</strong><div class="mini"><code>' + escapeHTML(domain.id) + '</code></div></div>'
        + '<span class="badge">' + escapeHTML(domain.created_at || 'created') + '</span></div>'
        + '<div class="domain-desc">' + escapeHTML(domain.description || '无说明') + '</div>'
        + '</div>').join('');
    }

    function renderNodes(nodes, domains) {
      const body = document.getElementById('nodesBody');
      if (!nodes.length) {
        body.innerHTML = '<tr><td colspan="8" class="empty">暂无节点。启动只配置 daemon_addr 的 agent 后，这里会出现待审批节点。</td></tr>';
        return;
      }
      body.innerHTML = nodes.map((node, idx) => {
        return '<tr>'
          + '<td><div class="node-name"><code>' + escapeHTML(node.id) + '</code><span class="mini">' + escapeHTML(node.name || '') + '</span></div></td>'
          + '<td><code>' + escapeHTML(node.domain_id || '-') + '</code></td>'
          + '<td>' + escapeHTML(node.role || '-') + '</td>'
          + '<td><span class="badge ' + escapeAttr(node.status) + '">' + escapeHTML(node.status || '-') + '</span> ' + (node.approved ? '<span class="badge online">approved</span>' : '<span class="badge warning">pending</span>') + '</td>'
          + '<td>' + escapeHTML(node.node_type || node.platform || '-') + '<div class="mini">' + escapeHTML(node.agent_version || '') + '</div></td>'
          + '<td><code>' + escapeHTML(node.interface || '-') + '</code><div class="mini">' + escapeHTML(node.config_type || '') + ' ' + escapeHTML(node.reload_method || '') + '</div></td>'
          + '<td>' + escapeHTML(node.last_seen_at || '-') + '</td>'
          + '<td>' + approvalControls(node, idx, domains) + '</td>'
          + '</tr>';
      }).join('');
      body.querySelectorAll('[data-node-form]').forEach(el => {
        el.addEventListener('input', markNodeFormDirty);
        el.addEventListener('change', markNodeFormDirty);
      });
      body.querySelectorAll('select[data-role]').forEach(select => {
        updateNatterControls(select);
        select.addEventListener('change', () => updateNatterControls(select));
      });
      body.querySelectorAll('button[data-approve]').forEach(btn => {
        const idx = Number(btn.dataset.approve);
        btn.addEventListener('click', () => approveNode(nodes[idx], idx, btn));
      });
    }

    function approvalControls(node, idx, domains) {
      const id = 'approve-' + idx + '-';
      const domainOptions = domains.map(domain => '<option value="' + escapeValue(domain.id) + '"' + selected(domain.id, node.domain_id) + '>' + escapeHTML(domain.name || domain.id) + '</option>').join('');
      const defaultNodeType = node.node_type || inferNodeType(node.platform);
      const defaultConfigType = node.config_type || (defaultNodeType === 'openwrt' ? 'openwrt_uci' : 'wg_conf');
      const defaultReload = node.reload_method || (defaultNodeType === 'openwrt' ? 'ifup' : 'wg-quick-restart');
      const defaultNatterControl = node.natter_wireguard_control || (defaultNodeType === 'openwrt' ? 'ifup' : 'wg-quick');
      return '<div class="approval">'
        + '<input data-node-form id="' + id + 'name" placeholder="节点名称" value="' + escapeValue(node.name || '') + '">'
        + '<select data-node-form id="' + id + 'domain">' + domainOptions + '</select>'
        + '<select data-node-form id="' + id + 'role" data-role="' + idx + '"><option value="client"' + selected('client', node.role || 'client') + '>client</option><option value="server"' + selected('server', node.role) + '>server</option></select>'
        + '<select data-node-form id="' + id + 'nodeType"><option value="linux"' + selected('linux', defaultNodeType) + '>linux</option><option value="openwrt"' + selected('openwrt', defaultNodeType) + '>openwrt</option></select>'
        + '<input data-node-form id="' + id + 'interface" placeholder="wg0" value="' + escapeValue(node.interface || 'wg0') + '">'
        + '<select data-node-form id="' + id + 'configType"><option value="wg_conf"' + selected('wg_conf', defaultConfigType) + '>wg_conf</option><option value="openwrt_uci"' + selected('openwrt_uci', defaultConfigType) + '>openwrt_uci</option><option value="runtime"' + selected('runtime', defaultConfigType) + '>runtime</option></select>'
        + '<select data-node-form id="' + id + 'reloadMethod"><option value="wg-quick-restart"' + selected('wg-quick-restart', defaultReload) + '>wg-quick-restart</option><option value="ifup"' + selected('ifup', defaultReload) + '>ifup</option><option value="none"' + selected('none', defaultReload) + '>none</option></select>'
        + '<div id="' + id + 'natterGroup" class="natter-fields">'
        + '<input data-node-form id="' + id + 'natterCommand" placeholder="server 可填: python3 /opt/Natter/natter.py -u -i pppoe-wan -b 51820 --map-only" value="' + escapeValue((node.natter_command || []).join(' ')) + '">'
        + '<input data-node-form id="' + id + 'natterTimeout" type="number" min="1" placeholder="Natter timeout" value="' + escapeValue(node.natter_timeout_seconds || 90) + '">'
        + '<label class="check"><input data-node-form id="' + id + 'natterStop" type="checkbox" ' + (node.natter_stop_wireguard ? 'checked' : '') + '>停 WG</label>'
        + '<select data-node-form id="' + id + 'natterControl"><option value="ifup"' + selected('ifup', defaultNatterControl) + '>ifup</option><option value="wg-quick"' + selected('wg-quick', defaultNatterControl) + '>wg-quick</option><option value="systemd"' + selected('systemd', defaultNatterControl) + '>systemd</option></select>'
        + '<input data-node-form id="' + id + 'natterRestartDelay" type="number" min="0" placeholder="restart delay" value="' + escapeValue(node.natter_restart_delay_seconds || 0) + '">'
        + '</div>'
        + '<button data-approve="' + idx + '">' + (node.approved ? '保存配置' : '允许加入') + '</button>'
        + '</div>';
    }

    function updateNatterControls(select) {
      const group = field('approve-' + select.dataset.role + '-natterGroup');
      if (!group) return;
      group.hidden = select.value !== 'server';
    }

    function markNodeFormDirty() {
      nodeFormDirty = true;
      if (!autoRefreshPaused) {
        document.getElementById('subtitle').textContent = '正在编辑节点配置，自动刷新会暂时跳过。';
      }
    }

    function isEditingNodeConfig() {
      const active = document.activeElement;
      return !!active && !!active.closest && !!active.closest('#nodesBody');
    }

    function toggleAutoRefresh() {
      autoRefreshPaused = !autoRefreshPaused;
      pauseRefreshBtn.textContent = autoRefreshPaused ? '恢复自动刷新' : '暂停自动刷新';
      if (autoRefreshPaused) {
        clearRefreshTimer();
        document.getElementById('subtitle').textContent = '自动刷新已暂停。';
        toast('自动刷新已暂停');
      } else {
        nodeFormDirty = false;
        toast('自动刷新已恢复');
        refresh(false);
      }
    }

    function renderInterfaces(interfaces) {
      const body = document.getElementById('interfacesBody');
      if (!interfaces.length) {
        body.innerHTML = '<tr><td colspan="7" class="empty">暂无 WireGuard inventory。agent 需要能执行 wg show，或至少配置 wireguard.name 后等待下一次心跳。</td></tr>';
        return;
      }
      body.innerHTML = interfaces.map(item => {
        const peers = (item.peers || []).map(peer => '<code>' + escapeHTML(shortKey(peer)) + '</code>').join(' ');
        return '<tr>'
          + '<td><code>' + escapeHTML(item.node_id) + '</code></td>'
          + '<td><code>' + escapeHTML(item.name) + '</code></td>'
          + '<td><code>' + escapeHTML(shortKey(item.public_key || '')) + '</code></td>'
          + '<td>' + (item.listen_port ? escapeHTML(item.listen_port) : '<span class="muted">-</span>') + '</td>'
          + '<td class="wrap">' + (peers || '<span class="muted">无 peer</span>') + '</td>'
          + '<td><span class="badge">' + escapeHTML(item.config_type || 'runtime') + '</span> <span class="muted">' + escapeHTML(item.config_path || '') + '</span></td>'
          + '<td>' + escapeHTML(item.updated_at || '-') + '</td>'
          + '</tr>';
      }).join('');
    }

    function renderBindings(bindings) {
      const body = document.getElementById('bindingsBody');
      if (!bindings.length) {
        body.innerHTML = '<tr><td colspan="6" class="empty">暂无绑定</td></tr>';
        return;
      }
      body.innerHTML = bindings.map((binding, idx) => {
        const endpoint = binding.endpoint_host
          ? '<code>' + escapeHTML(binding.endpoint_host + ':' + binding.endpoint_port) + '</code>'
          : '<span class="muted">未发布</span>';
        return '<tr>'
          + '<td><code>' + escapeHTML(binding.id) + '</code></td>'
          + '<td><code>' + escapeHTML(binding.server_node_id) + '</code> / <code>' + escapeHTML(binding.server_interface) + '</code></td>'
          + '<td><code>' + escapeHTML(binding.client_node_id) + '</code> / <code>' + escapeHTML(binding.client_interface) + '</code></td>'
          + '<td>' + endpoint + '</td>'
          + '<td><span class="badge">' + escapeHTML(binding.config_type) + '</span> <span class="muted">' + escapeHTML(binding.reload_method) + '</span></td>'
          + '<td><button data-binding="' + idx + '">触发 natter</button></td>'
          + '</tr>';
      }).join('');
      body.querySelectorAll('button[data-binding]').forEach(btn => {
        const binding = bindings[Number(btn.dataset.binding)];
        btn.addEventListener('click', () => runNatter(binding.server_node_id, binding.server_interface, btn));
      });
    }

    function renderEvents(events) {
      const body = document.getElementById('eventsBody');
      if (!events.length) {
        body.innerHTML = '<tr><td colspan="7" class="empty">暂无事件</td></tr>';
        return;
      }
      body.innerHTML = events.map(event => '<tr>'
        + '<td>' + escapeHTML(event.created_at) + '</td>'
        + '<td><span class="' + escapeAttr(event.severity) + '">' + escapeHTML(event.severity) + '</span></td>'
        + '<td><code>' + escapeHTML(event.type) + '</code></td>'
        + '<td>' + escapeHTML(event.node_id) + '</td>'
        + '<td>' + escapeHTML(event.binding_id) + '</td>'
        + '<td class="wrap">' + escapeHTML(event.message) + '</td>'
        + '<td class="wrap"><code>' + escapeHTML(JSON.stringify(event.payload || {})) + '</code></td>'
        + '</tr>').join('');
    }

    function showJoinResult(domain) {
      const el = document.getElementById('joinResult');
      if (!domain || !domain.join_code) {
        el.style.display = 'none';
        el.textContent = '';
        return;
      }
      el.innerHTML = 'Domain <code>' + escapeHTML(domain.id) + '</code> 已创建。普通节点只需要配置 daemon_addr；需要预绑定到这个 domain 时可额外填写 join_code: <code>' + escapeHTML(domain.join_code) + '</code>';
      el.style.display = 'block';
    }

    function field(id) {
      return document.getElementById(id);
    }

    function selected(value, current) {
      return String(value || '') === String(current || '') ? ' selected' : '';
    }

    function inferNodeType(platform) {
      const value = String(platform || '').toLowerCase();
      if (value.includes('openwrt')) return 'openwrt';
      return 'linux';
    }

    function splitCommand(value) {
      const input = String(value || '').trim();
      if (!input) return [];
      const parts = [];
      let current = '';
      let quote = '';
      let escaped = false;
      for (const ch of input) {
        if (escaped) {
          current += ch;
          escaped = false;
          continue;
        }
        if (ch === '\\') {
          escaped = true;
          continue;
        }
        if (quote) {
          if (ch === quote) quote = '';
          else current += ch;
          continue;
        }
        if (ch === '"' || ch === "'") {
          quote = ch;
          continue;
        }
        if (/\s/.test(ch)) {
          if (current) {
            parts.push(current);
            current = '';
          }
          continue;
        }
        current += ch;
      }
      if (escaped) current += '\\';
      if (current) parts.push(current);
      return parts;
    }

    function numberValue(value) {
      const n = Number(value);
      return Number.isFinite(n) && n > 0 ? n : 0;
    }

    function shortKey(value) {
      value = String(value || '');
      if (value.length <= 14) return value;
      return value.slice(0, 7) + '...' + value.slice(-6);
    }

    function scheduleRefresh() {
      clearRefreshTimer();
      if (!autoRefreshPaused) {
        refreshTimer = setTimeout(() => refresh(false, {auto: true}), 15000);
      }
    }

    function clearRefreshTimer() {
      if (refreshTimer) {
        clearTimeout(refreshTimer);
        refreshTimer = null;
      }
    }

    function setBusy(busy) {
      connectBtn.disabled = busy;
      refreshBtn.disabled = busy;
      pauseRefreshBtn.disabled = busy;
    }

    function toast(text) {
      const el = document.getElementById('toast');
      el.textContent = text;
      el.style.display = 'block';
      clearTimeout(window.__toastTimer);
      window.__toastTimer = setTimeout(() => el.style.display = 'none', 5000);
    }

    function escapeHTML(value) {
      return String(value || '').replace(/[&<>"']/g, ch => ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      }[ch]));
    }

    function escapeAttr(value) {
      return escapeHTML(value).replace(/[^a-zA-Z0-9_-]/g, '');
    }

    function escapeValue(value) {
      return escapeHTML(value);
    }
  </script>
</body>
</html>`
