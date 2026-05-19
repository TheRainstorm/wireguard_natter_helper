package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
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
	DaemonAddr  string
	GeneratedAt string
	Nodes       []store.Node
	Bindings    []store.Binding
	Events      []store.Event
	Stats       stats
}

type stats struct {
	Nodes        int
	Online       int
	Bindings     int
	WithEndpoint int
	Errors       int
}

func New(addr, daemonAddr, adminToken string) *Server {
	return &Server{addr: addr, daemonAddr: daemonAddr, adminToken: adminToken}
}

func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/summary", s.apiSummary)
	mux.HandleFunc("/api/run-natter", s.apiRunNatter)
	log.Printf("wgnh web ui listening on http://%s; daemon tcp=%s", s.addr, s.daemonAddr)
	return http.ListenAndServe(s.addr, mux)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := s.load(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, data); err != nil {
		log.Printf("render web ui failed: %v", err)
	}
}

func (s *Server) apiSummary(w http.ResponseWriter, r *http.Request) {
	data, err := s.load(r.Context())
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
		ServerNodeID    string `json:"server_node_id"`
		ServerInterface string `json:"server_interface"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.ServerNodeID == "" || req.ServerInterface == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "server_node_id and server_interface are required"})
		return
	}
	resp, err := rpc.Call(r.Context(), s.daemonAddr, rpc.Request{
		Kind:            "admin.run_natter",
		AdminToken:      s.adminToken,
		ServerNodeID:    req.ServerNodeID,
		ServerInterface: req.ServerInterface,
	}, 10*time.Second)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) load(ctx context.Context) (dashboardData, error) {
	nodesResp, err := rpc.Call(ctx, s.daemonAddr, rpc.Request{Kind: "admin.nodes", AdminToken: s.adminToken}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load nodes: %w", err)
	}
	bindingsResp, err := rpc.Call(ctx, s.daemonAddr, rpc.Request{Kind: "admin.bindings", AdminToken: s.adminToken}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load bindings: %w", err)
	}
	eventsResp, err := rpc.Call(ctx, s.daemonAddr, rpc.Request{Kind: "admin.events", AdminToken: s.adminToken, Limit: 80}, 10*time.Second)
	if err != nil {
		return dashboardData{}, fmt.Errorf("load events: %w", err)
	}

	nodes := nodesResp.Nodes
	bindings := bindingsResp.Bindings
	events := eventsResp.Events
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })

	st := stats{Nodes: len(nodes), Bindings: len(bindings)}
	for _, node := range nodes {
		if node.Status == "online" {
			st.Online++
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
		DaemonAddr:  s.daemonAddr,
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		Nodes:       nodes,
		Bindings:    bindings,
		Events:      events,
		Stats:       st,
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
    .stats {
      display: grid;
      grid-template-columns: repeat(5, minmax(140px, 1fr));
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
      padding: 8px 11px;
      font-weight: 650;
      cursor: pointer;
    }
    button.secondary { background: var(--accent-2); }
    button:disabled { opacity: .55; cursor: wait; }
    .toolbar { display: flex; gap: 8px; align-items: center; }
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
    @media (max-width: 900px) {
      .top { align-items: flex-start; flex-direction: column; }
      .stats { grid-template-columns: repeat(2, minmax(130px, 1fr)); }
      main { padding: 16px; }
    }
  </style>
</head>
<body>
  <header>
    <div class="top">
      <div>
        <h1>WireGuard Natter Helper</h1>
        <div class="sub">daemon <code>{{.DaemonAddr}}</code> · {{.GeneratedAt}}</div>
      </div>
      <div class="toolbar">
        <button class="secondary" onclick="location.reload()">刷新</button>
      </div>
    </div>
  </header>
  <main>
    <div class="stats">
      <div class="stat"><div class="label">节点</div><div class="value">{{.Stats.Nodes}}</div></div>
      <div class="stat"><div class="label">在线节点</div><div class="value">{{.Stats.Online}}</div></div>
      <div class="stat"><div class="label">绑定</div><div class="value">{{.Stats.Bindings}}</div></div>
      <div class="stat"><div class="label">已有 Endpoint</div><div class="value">{{.Stats.WithEndpoint}}</div></div>
      <div class="stat"><div class="label">错误事件</div><div class="value">{{.Stats.Errors}}</div></div>
    </div>

    <section>
      <div class="section-head"><h2>节点</h2></div>
      <div class="scroll">
        <table>
          <thead><tr><th>节点</th><th>角色</th><th>状态</th><th>平台</th><th>版本</th><th>最后心跳</th></tr></thead>
          <tbody>
            {{range .Nodes}}
            <tr>
              <td><code>{{.ID}}</code></td>
              <td>{{.Role}}</td>
              <td><span class="badge {{.Status}}">{{.Status}}</span></td>
              <td>{{.Platform}}</td>
              <td>{{.AgentVersion}}</td>
              <td>{{.LastSeenAt}}</td>
            </tr>
            {{else}}
            <tr><td colspan="6" class="empty">暂无节点</td></tr>
            {{end}}
          </tbody>
        </table>
      </div>
    </section>

    <section>
      <div class="section-head"><h2>WireGuard 绑定</h2></div>
      <div class="scroll">
        <table>
          <thead><tr><th>绑定</th><th>Server</th><th>Client</th><th>Endpoint</th><th>配置</th><th>操作</th></tr></thead>
          <tbody>
            {{range .Bindings}}
            <tr>
              <td><code>{{.ID}}</code></td>
              <td><code>{{.ServerNodeID}}</code> / <code>{{.ServerInterface}}</code></td>
              <td><code>{{.ClientNodeID}}</code> / <code>{{.ClientInterface}}</code></td>
              <td>{{if .EndpointHost}}<code>{{.EndpointHost}}:{{.EndpointPort}}</code>{{else}}<span class="muted">未发布</span>{{end}}</td>
              <td><span class="badge">{{.ConfigType}}</span> <span class="muted">{{.ReloadMethod}}</span></td>
              <td><button onclick="runNatter('{{.ServerNodeID}}','{{.ServerInterface}}', this)">触发 natter</button></td>
            </tr>
            {{else}}
            <tr><td colspan="6" class="empty">暂无绑定</td></tr>
            {{end}}
          </tbody>
        </table>
      </div>
    </section>

    <section>
      <div class="section-head"><h2>最近事件</h2></div>
      <div class="scroll">
        <table>
          <thead><tr><th>时间</th><th>级别</th><th>类型</th><th>节点</th><th>绑定</th><th>消息</th></tr></thead>
          <tbody>
            {{range .Events}}
            <tr>
              <td>{{.CreatedAt}}</td>
              <td><span class="{{.Severity}}">{{.Severity}}</span></td>
              <td><code>{{.Type}}</code></td>
              <td>{{.NodeID}}</td>
              <td>{{.BindingID}}</td>
              <td class="wrap">{{.Message}}</td>
            </tr>
            {{else}}
            <tr><td colspan="6" class="empty">暂无事件</td></tr>
            {{end}}
          </tbody>
        </table>
      </div>
    </section>
  </main>
  <div id="toast" class="toast"></div>
  <script>
    function toast(text) {
      const el = document.getElementById('toast');
      el.textContent = text;
      el.style.display = 'block';
      clearTimeout(window.__toastTimer);
      window.__toastTimer = setTimeout(() => el.style.display = 'none', 5000);
    }
    async function runNatter(serverNodeID, serverInterface, btn) {
      btn.disabled = true;
      try {
        const res = await fetch('/api/run-natter', {
          method: 'POST',
          headers: {'Content-Type': 'application/json'},
          body: JSON.stringify({server_node_id: serverNodeID, server_interface: serverInterface})
        });
        const data = await res.json();
        if (!res.ok || !data.ok) throw new Error(data.error || 'request failed');
        toast('已下发 natter.run: ' + data.command.command_id);
      } catch (err) {
        toast('失败: ' + err.message);
      } finally {
        btn.disabled = false;
      }
    }
  </script>
</body>
</html>`
