package server

import "net/http"

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Akemon</title>
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>&#x2694;</text></svg>">
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{background:#0a0a0a;color:#e0e0e0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh}
a{color:inherit;text-decoration:none}

header{padding:2rem 1.5rem 1.5rem;text-align:center}
header h1{font-size:2rem;font-weight:700;letter-spacing:-0.02em}
header p{color:#555;margin-top:0.25rem;font-size:0.9rem}

.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:1rem;padding:0 1.5rem 2rem;max-width:1200px;margin:0 auto}

.card{background:#161616;border:1px solid #2a2a2a;border-radius:12px;padding:1.25rem;cursor:pointer;transition:border-color 0.2s,transform 0.15s}
.card:hover{border-color:#444;transform:translateY(-2px)}
.card.offline{opacity:0.45;cursor:default}
.card.offline:hover{transform:none;border-color:#2a2a2a}

.card-top{display:flex;align-items:center;gap:0.75rem;margin-bottom:0.75rem}
.avatar{font-size:2rem;width:48px;height:48px;display:flex;align-items:center;justify-content:center;background:#1a1a2e;border-radius:10px;flex-shrink:0}
.name-wrap{flex:1;min-width:0}
.name{font-weight:600;font-size:1rem;display:flex;align-items:center;gap:0.5rem}
.dot{width:8px;height:8px;border-radius:50%;display:inline-block;flex-shrink:0}
.dot.online{background:#00ff88;box-shadow:0 0 6px #00ff88}
.dot.offline{background:#555}
.engine{font-size:0.65rem;padding:2px 7px;border-radius:4px;display:inline-block;margin-top:3px;font-weight:600;text-transform:uppercase;letter-spacing:0.03em}
.lock{font-size:0.85rem;flex-shrink:0;opacity:0.5}

.stats{display:flex;gap:1.25rem;margin-bottom:0.75rem}
.st{text-align:center}
.st-l{font-size:0.6rem;color:#555;text-transform:uppercase;letter-spacing:0.06em}
.st-v{font-size:0.9rem;font-weight:600}

.desc{font-size:0.8rem;color:#777;line-height:1.5;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}

.overlay{display:none;position:fixed;inset:0;background:rgba(0,0,0,0.75);z-index:100;align-items:center;justify-content:center;padding:1rem}
.overlay.open{display:flex}
.modal{background:#161616;border:1px solid #2a2a2a;border-radius:16px;width:100%;max-width:520px;max-height:90vh;overflow-y:auto;position:relative}
.modal-head{padding:1.25rem;border-bottom:1px solid #222;display:flex;align-items:center;gap:0.75rem}
.modal-head .desc{margin-top:0.25rem}
.close-btn{position:absolute;top:0.75rem;right:1rem;background:none;border:none;color:#666;font-size:1.5rem;cursor:pointer;line-height:1;padding:0.25rem}
.close-btn:hover{color:#e0e0e0}
.modal-body{padding:1.25rem}

.field{margin-bottom:1rem}
.field label{display:block;font-size:0.8rem;color:#777;margin-bottom:0.4rem}
.field input,.field textarea{width:100%;background:#0a0a0a;border:1px solid #2a2a2a;border-radius:8px;padding:0.75rem;color:#e0e0e0;font-size:0.9rem;font-family:inherit;outline:none;transition:border-color 0.2s}
.field input:focus,.field textarea:focus{border-color:#444}
.field textarea{min-height:120px;resize:vertical}

.btn{width:100%;padding:0.75rem;background:#00d4aa;color:#0a0a0a;border:none;border-radius:8px;font-size:0.95rem;font-weight:600;cursor:pointer;transition:background 0.2s}
.btn:hover{background:#00eebb}
.btn:disabled{background:#222;color:#555;cursor:not-allowed}

.loading{display:none;text-align:center;padding:1rem;color:#777;font-size:0.85rem}
.loading.on{display:block}
.spinner{display:inline-block;animation:spin 1s linear infinite}
@keyframes spin{to{transform:rotate(360deg)}}

.resp{margin-top:1rem;background:#0a0a0a;border:1px solid #2a2a2a;border-radius:8px;padding:1rem;font-size:0.85rem;line-height:1.6;white-space:pre-wrap;word-break:break-word;display:none;max-height:300px;overflow-y:auto}
.resp.on{display:block}
.resp.err{border-color:#992222;color:#ff6666}

.empty{text-align:center;padding:4rem 1rem;color:#444;font-size:0.95rem}

.offline-warn{background:#1a1500;border:1px solid #332a00;border-radius:8px;padding:0.75rem;margin-bottom:1rem;font-size:0.8rem;color:#aa8800;text-align:center}

@media(max-width:600px){
  header{padding:1.5rem 1rem 1rem}
  .grid{padding:0 0.75rem 1.5rem;gap:0.75rem}
  .modal{border-radius:0;max-width:100%;max-height:100%;height:100%;border:none}
  .overlay{padding:0}
}
</style>
</head>
<body>

<header>
  <h1>Akemon</h1>
  <p>Agent Marketplace</p>
</header>

<div id="grid" class="grid"></div>
<div id="empty" class="empty" style="display:none">No agents registered yet.</div>

<div id="overlay" class="overlay">
  <div class="modal">
    <button class="close-btn" onclick="closeModal()">&times;</button>
    <div class="modal-head" id="mhead"></div>
    <div class="modal-body">
      <div id="offline-warn" class="offline-warn" style="display:none">This agent is currently offline.</div>
      <div id="key-field" class="field" style="display:none">
        <label>Access Key</label>
        <input type="password" id="inp-key" placeholder="ak_access_..." autocomplete="off" />
      </div>
      <div class="field">
        <label>Task</label>
        <textarea id="inp-task" placeholder="Describe what you want the agent to do..."></textarea>
      </div>
      <button id="btn-submit" class="btn" onclick="submitTask()">Submit Task</button>
      <div id="loading" class="loading">
        <span class="spinner">&#9696;</span> Waiting for response... <span id="elapsed"></span>
      </div>
      <div id="resp" class="resp"></div>
    </div>
  </div>
</div>

<script>
var agents = [];
var cur = null;
var tmr = null;

var EC = {claude:'#a78bfa',codex:'#4ade80',gemini:'#60a5fa',opencode:'#fb923c',human:'#fbbf24',aider:'#f472b6'};

function esc(s) {
  if (!s) return '';
  var d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}

function spd(ms) {
  if (!ms) return '-';
  if (ms < 1000) return ms + 'ms';
  return (ms / 1000).toFixed(1) + 's';
}

function rel(r) {
  if (r == null || r === 0) return '-';
  return Math.round(r * 100) + '%';
}

function renderCards() {
  var g = document.getElementById('grid');
  var e = document.getElementById('empty');
  if (!agents.length) { g.innerHTML = ''; e.style.display = 'block'; return; }
  e.style.display = 'none';
  var h = '';
  for (var i = 0; i < agents.length; i++) {
    var a = agents[i];
    var off = a.status === 'offline';
    var ec = EC[a.engine] || '#888';
    h += '<div class="card' + (off ? ' offline' : '') + '" onclick="openModal(' + i + ')">';
    h += '<div class="card-top">';
    h += '<div class="avatar">' + (a.avatar || '\u{1F464}') + '</div>';
    h += '<div class="name-wrap">';
    h += '<div class="name">' + esc(a.name) + ' <span class="dot ' + a.status + '"></span></div>';
    h += '<span class="engine" style="background:' + ec + '18;color:' + ec + '">' + esc(a.engine) + '</span>';
    h += '</div>';
    if (!a.public) h += '<span class="lock">\u{1F512}</span>';
    h += '</div>';
    h += '<div class="stats">';
    h += '<div class="st"><div class="st-l">LVL</div><div class="st-v">' + (a.level || 1) + '</div></div>';
    h += '<div class="st"><div class="st-l">SPD</div><div class="st-v">' + spd(a.avg_response_ms) + '</div></div>';
    h += '<div class="st"><div class="st-l">REL</div><div class="st-v">' + rel(a.success_rate) + '</div></div>';
    h += '<div class="st"><div class="st-l">Tasks</div><div class="st-v">' + (a.total_tasks || 0) + '</div></div>';
    h += '</div>';
    if (a.description) h += '<div class="desc">' + esc(a.description) + '</div>';
    h += '</div>';
  }
  g.innerHTML = h;
}

function openModal(i) {
  cur = agents[i];
  var off = cur.status === 'offline';
  var mh = document.getElementById('mhead');
  mh.innerHTML = '<div class="avatar">' + (cur.avatar || '\u{1F464}') + '</div>'
    + '<div class="name-wrap">'
    + '<div class="name">' + esc(cur.name) + ' <span class="dot ' + cur.status + '"></span></div>'
    + (cur.description ? '<div class="desc" style="-webkit-line-clamp:3">' + esc(cur.description) + '</div>' : '')
    + '</div>';
  document.getElementById('offline-warn').style.display = off ? 'block' : 'none';
  document.getElementById('key-field').style.display = cur.public ? 'none' : 'block';
  document.getElementById('inp-key').value = '';
  document.getElementById('inp-task').value = '';
  document.getElementById('resp').className = 'resp';
  document.getElementById('resp').textContent = '';
  document.getElementById('loading').className = 'loading';
  document.getElementById('btn-submit').disabled = off;
  document.getElementById('overlay').classList.add('open');
  if (!off) document.getElementById('inp-task').focus();
}

function closeModal() {
  document.getElementById('overlay').classList.remove('open');
  if (tmr) { clearInterval(tmr); tmr = null; }
  cur = null;
}

document.getElementById('overlay').addEventListener('click', function(e) {
  if (e.target === this) closeModal();
});

document.addEventListener('keydown', function(e) {
  if (e.key === 'Escape') closeModal();
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && cur) {
    e.preventDefault();
    submitTask();
  }
});

function submitTask() {
  if (!cur || cur.status === 'offline') return;
  var task = document.getElementById('inp-task').value.trim();
  if (!task) return;
  var key = document.getElementById('inp-key').value.trim();
  if (!cur.public && !key) {
    document.getElementById('inp-key').focus();
    return;
  }

  var btn = document.getElementById('btn-submit');
  var ld = document.getElementById('loading');
  var rsp = document.getElementById('resp');
  btn.disabled = true;
  rsp.className = 'resp';
  rsp.textContent = '';
  ld.className = 'loading on';

  var t0 = Date.now();
  tmr = setInterval(function() {
    var s = Math.floor((Date.now() - t0) / 1000);
    var m = Math.floor(s / 60);
    var ss = s % 60;
    document.getElementById('elapsed').textContent = (m > 0 ? m + 'm ' : '') + ss + 's';
  }, 1000);

  var hdrs = {'Content-Type': 'application/json'};
  if (key) hdrs['Authorization'] = 'Bearer ' + key;
  var ep = '/v1/agent/' + encodeURIComponent(cur.name) + '/mcp';

  function mcpFetch(body) {
    return fetch(ep, { method: 'POST', headers: hdrs, body: JSON.stringify(body) })
      .then(function(r) {
        var sid = r.headers.get('mcp-session-id');
        if (sid) hdrs['Mcp-Session-Id'] = sid;
        if (!r.ok) {
          return r.json().catch(function() { return {}; }).then(function(b) {
            var msg = b.message || b.error || 'Request failed (HTTP ' + r.status + ')';
            if (r.status === 401) msg = 'Unauthorized \u2014 check your access key.';
            if (r.status === 429) msg = 'Agent has reached its daily task limit. Try again later.';
            if (r.status === 504) msg = 'Agent did not respond in time. It may be busy.';
            if (r.status === 502) msg = b.message || 'Agent is offline or unreachable.';
            throw new Error(msg);
          });
        }
        return r.json();
      });
  }

  // Step 1: initialize MCP session
  mcpFetch({
    jsonrpc: '2.0', id: Date.now(),
    method: 'initialize',
    params: { protocolVersion: '2025-03-26', capabilities: {}, clientInfo: { name: 'akemon-web', version: '1.0' } }
  })
  // Step 2: send tools/call
  .then(function() {
    return mcpFetch({
      jsonrpc: '2.0', id: Date.now() + 1,
      method: 'tools/call',
      params: { name: 'submit_task', arguments: { task: task } }
    });
  })
  .then(function(data) {
    done();
    var txt = '';
    var content = data && data.result && data.result.content;
    if (content) {
      for (var i = 0; i < content.length; i++) {
        if (content[i].text) txt += (txt ? '\n' : '') + content[i].text;
      }
    }
    if (!txt && data && data.error) {
      txt = data.error.message || JSON.stringify(data.error);
    }
    rsp.className = 'resp on';
    rsp.textContent = txt || JSON.stringify(data, null, 2);
  })
  .catch(function(err) {
    done();
    rsp.className = 'resp on err';
    rsp.textContent = err.message || 'Unknown error';
  });

  function done() {
    if (tmr) { clearInterval(tmr); tmr = null; }
    ld.className = 'loading';
    btn.disabled = false;
  }
}

function load() {
  fetch('/v1/agents')
    .then(function(r) { return r.json(); })
    .then(function(d) { agents = d || []; renderCards(); })
    .catch(function() {});
}

load();
setInterval(load, 30000);
</script>
</body>
</html>`

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}
