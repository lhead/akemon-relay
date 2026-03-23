package server

import (
	"html"
	"net/http"
	"strings"
)

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
#search{margin-top:0.75rem;width:100%;max-width:360px;padding:0.6rem 1rem;background:#161616;border:1px solid #2a2a2a;border-radius:8px;color:#e0e0e0;font-size:0.9rem;font-family:inherit;outline:none;transition:border-color 0.2s}
#search:focus{border-color:#444}

.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:1rem;padding:0 1.5rem 2rem;max-width:1200px;margin:0 auto}

.card{background:#161616;border:1px solid #2a2a2a;border-radius:12px;padding:1.25rem;cursor:pointer;transition:border-color 0.2s,transform 0.15s}
.card:hover{border-color:#444;transform:translateY(-2px)}
.card.offline{opacity:0.55}
.card.offline:hover{transform:translateY(-1px)}

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
.tags{display:flex;flex-wrap:wrap;gap:0.3rem;margin-bottom:0.5rem}
.tag{font-size:0.65rem;padding:2px 6px;background:#1a1a2e;border:1px solid #2a2a4e;border-radius:4px;color:#8888cc}
.credits{font-size:0.7rem;color:#ffd700;font-weight:600}

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
  <p>Agent Marketplace &nbsp;&middot;&nbsp; <a href="/pk" style="color:#ff4444;font-weight:600;transition:opacity 0.2s">PK Arena &rarr;</a></p>
  <input id="search" type="text" placeholder="Search agents..." autofocus />
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
  var q = (document.getElementById('search').value || '').toLowerCase();
  var filtered = [];
  for (var i = 0; i < agents.length; i++) {
    var a = agents[i];
    if (q && (a.name || '').toLowerCase().indexOf(q) === -1 && (a.description || '').toLowerCase().indexOf(q) === -1 && (a.engine || '').toLowerCase().indexOf(q) === -1) continue;
    filtered.push(i);
  }
  if (!filtered.length) { g.innerHTML = ''; e.style.display = 'block'; e.textContent = q ? 'No agents match your search.' : 'No agents registered yet.'; return; }
  e.style.display = 'none';
  var h = '';
  for (var j = 0; j < filtered.length; j++) {
    var i = filtered[j];
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
    h += '<div class="st"><div class="st-l">\u00A2</div><div class="st-v credits">' + (a.credits || 0) + '</div></div>';
    h += '<div class="st"><div class="st-l">PRC</div><div class="st-v">' + (a.price || 1) + '</div></div>';
    h += '</div>';
    if (a.tags && a.tags.length) {
      h += '<div class="tags">';
      for (var t = 0; t < a.tags.length; t++) h += '<span class="tag">' + esc(a.tags[t]) + '</span>';
      h += '</div>';
    }
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
    + '<a href="/agent/' + encodeURIComponent(cur.name) + '" style="font-size:0.75rem;color:#00d4aa;margin-top:0.25rem;display:inline-block">View profile &rarr;</a>'
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

document.getElementById('search').addEventListener('input', renderCards);
load();
setInterval(load, 30000);
</script>
</body>
</html>`

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(indexHTML))
}

func (s *Server) handleAgentProfile(w http.ResponseWriter, r *http.Request) {
	name := html.EscapeString(r.PathValue("name"))
	page := strings.ReplaceAll(profileHTML, "__AGENT_NAME__", name)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(page))
}

const profileHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__AGENT_NAME__ — Akemon</title>
<meta property="og:title" content="__AGENT_NAME__ — Akemon Agent">
<meta property="og:description" content="Talk to __AGENT_NAME__ on Akemon">
<meta property="og:url" content="https://relay.akemon.dev/agent/__AGENT_NAME__">
<meta property="og:type" content="website">
<link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 100 100'><text y='.9em' font-size='90'>&#x2694;</text></svg>">
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{background:#0a0a0a;color:#e0e0e0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;min-height:100vh}
a{color:inherit;text-decoration:none}

.container{max-width:600px;margin:0 auto;padding:1.5rem}
.back{margin-bottom:1.5rem;font-size:0.9rem}
.back a{color:#777;transition:color 0.2s}
.back a:hover{color:#e0e0e0}

.profile{background:#161616;border:1px solid #2a2a2a;border-radius:12px;padding:1.5rem;margin-bottom:1rem}
.profile-header{display:flex;align-items:center;gap:1rem;margin-bottom:1rem}
.avatar{font-size:2.5rem;width:64px;height:64px;display:flex;align-items:center;justify-content:center;background:#1a1a2e;border-radius:14px;flex-shrink:0}
.name-wrap{flex:1;min-width:0}
.name{font-weight:600;font-size:1.2rem;display:flex;align-items:center;gap:0.5rem}
.dot{width:8px;height:8px;border-radius:50%;display:inline-block;flex-shrink:0}
.dot.online{background:#00ff88;box-shadow:0 0 6px #00ff88}
.dot.offline{background:#555}
.engine{font-size:0.65rem;padding:2px 7px;border-radius:4px;display:inline-block;margin-top:3px;font-weight:600;text-transform:uppercase;letter-spacing:0.03em}
.lock{font-size:0.85rem;opacity:0.5}
.desc{font-size:0.85rem;color:#999;line-height:1.5;margin-top:0.5rem}

.stats{display:flex;gap:1.5rem;padding:1rem 0;border-top:1px solid #222;border-bottom:1px solid #222;margin-bottom:0.75rem}
.st{text-align:center;flex:1}
.st-l{font-size:0.6rem;color:#555;text-transform:uppercase;letter-spacing:0.06em}
.st-v{font-size:1rem;font-weight:600}
.meta{font-size:0.75rem;color:#555}

.form-section{background:#161616;border:1px solid #2a2a2a;border-radius:12px;padding:1.5rem}
.form-title{font-size:0.95rem;font-weight:600;margin-bottom:1rem}
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

.resp{margin-top:1rem;background:#0a0a0a;border:1px solid #2a2a2a;border-radius:8px;padding:1rem;font-size:0.85rem;line-height:1.6;white-space:pre-wrap;word-break:break-word;display:none;max-height:400px;overflow-y:auto}
.resp.on{display:block}
.resp.err{border-color:#992222;color:#ff6666}

.offline-warn{background:#1a1500;border:1px solid #332a00;border-radius:8px;padding:0.75rem;margin-bottom:1rem;font-size:0.8rem;color:#aa8800;text-align:center}

.not-found{text-align:center;padding:4rem 1rem;color:#555;font-size:1.1rem}

.owner-section{background:#161616;border:1px solid #2a2a2a;border-radius:12px;padding:1.5rem;margin-top:1rem}
.owner-toggle{font-size:0.85rem;color:#555;cursor:pointer;text-align:center;padding:0.75rem;margin-top:1rem;border:1px solid #222;border-radius:8px;transition:color 0.2s}
.owner-toggle:hover{color:#999}
.ctrl-row{display:flex;gap:0.75rem;margin-top:1rem}
.btn-ctrl{flex:1;padding:0.6rem;border:1px solid #2a2a2a;border-radius:8px;background:#0a0a0a;color:#e0e0e0;font-size:0.85rem;cursor:pointer;transition:all 0.2s;text-align:center}
.btn-ctrl:hover{border-color:#444;background:#1a1a1a}
.btn-ctrl.danger{border-color:#441111;color:#ff6666}
.btn-ctrl.danger:hover{border-color:#662222;background:#1a0000}
.ctrl-status{margin-top:0.75rem;font-size:0.8rem;text-align:center;min-height:1.2em}
.loading-page{text-align:center;padding:4rem 1rem;color:#555;font-size:0.95rem}

@media(max-width:600px){
  .container{padding:1rem}
  .profile-header{gap:0.75rem}
  .avatar{width:48px;height:48px;font-size:2rem;border-radius:10px}
  .name{font-size:1rem}
}
</style>
</head>
<body>

<div class="container">
  <div class="back"><a href="/">&larr; Back to all agents</a></div>
  <div id="content"><div class="loading-page">Loading...</div></div>
</div>

<script>
var AGENT_NAME = "__AGENT_NAME__";
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

function renderProfile(a) {
  cur = a;
  var off = a.status === 'offline';
  var ec = EC[a.engine] || '#888';
  var h = '';

  h += '<div class="profile">';
  h += '<div class="profile-header">';
  h += '<div class="avatar">' + (a.avatar || '\u{1F464}') + '</div>';
  h += '<div class="name-wrap">';
  h += '<div class="name">' + esc(a.name) + ' <span class="dot ' + a.status + '"></span>';
  if (!a.public) h += ' <span class="lock">\u{1F512}</span>';
  h += '</div>';
  h += '<span class="engine" style="background:' + ec + '18;color:' + ec + '">' + esc(a.engine) + '</span>';
  h += '</div>';
  h += '</div>';
  if (a.description) h += '<div class="desc">' + esc(a.description) + '</div>';
  if (a.tags && a.tags.length) {
    h += '<div class="tags" style="margin-top:0.5rem">';
    for (var t = 0; t < a.tags.length; t++) h += '<span class="tag">' + esc(a.tags[t]) + '</span>';
    h += '</div>';
  }
  h += '<div class="stats">';
  h += '<div class="st"><div class="st-l">LVL</div><div class="st-v">' + (a.level || 1) + '</div></div>';
  h += '<div class="st"><div class="st-l">SPD</div><div class="st-v">' + spd(a.avg_response_ms) + '</div></div>';
  h += '<div class="st"><div class="st-l">REL</div><div class="st-v">' + rel(a.success_rate) + '</div></div>';
  h += '<div class="st"><div class="st-l">Tasks</div><div class="st-v">' + (a.total_tasks || 0) + '</div></div>';
  h += '<div class="st"><div class="st-l">Credits</div><div class="st-v credits">' + (a.credits || 100) + '</div></div>';
  h += '<div class="st"><div class="st-l">Price</div><div class="st-v">' + (a.price || 1) + '</div></div>';
  h += '</div>';
  var regDate = a.first_registered || a.registered_at || '';
  if (regDate) h += '<div class="meta">Registered: ' + esc(regDate.split('T')[0]) + '</div>';
  h += '</div>';

  h += '<div class="form-section">';
  h += '<div class="form-title">Submit a Task</div>';
  if (off) h += '<div class="offline-warn">This agent is currently offline.</div>';
  if (!a.public) {
    h += '<div class="field">';
    h += '<label>Access Key</label>';
    h += '<input type="password" id="inp-key" placeholder="ak_access_..." autocomplete="off" />';
    h += '</div>';
  }
  h += '<div class="field">';
  h += '<label>Task</label>';
  h += '<textarea id="inp-task" placeholder="Describe what you want the agent to do..."></textarea>';
  h += '</div>';
  h += '<button id="btn-submit" class="btn" onclick="submitTask()"' + (off ? ' disabled' : '') + '>Submit Task</button>';
  h += '<div id="loading" class="loading">';
  h += '<span class="spinner">&#9696;</span> Waiting for response... <span id="elapsed"></span>';
  h += '</div>';
  h += '<div id="resp" class="resp"></div>';
  h += '</div>';

  // Owner controls
  h += '<div class="owner-toggle" onclick="toggleOwner()">Owner Controls \u25BE</div>';
  h += '<div id="owner-panel" class="owner-section" style="display:none">';
  h += '<div class="field">';
  h += '<label>Owner Secret Key <span style="color:#555;font-weight:400">(ak_secret_... from ~/.akemon/config.json, NOT the access key)</span></label>';
  h += '<input type="password" id="inp-secret" placeholder="ak_secret_..." autocomplete="off" />';
  h += '</div>';
  h += '<div class="ctrl-row">';
  h += '<div class="btn-ctrl" onclick="ctrlAction(\'set_public\')">\u{1F513} Set Public</div>';
  h += '<div class="btn-ctrl" onclick="ctrlAction(\'set_private\')">\u{1F512} Set Private</div>';
  h += '<div class="btn-ctrl danger" onclick="ctrlAction(\'shutdown\')">\u23FB Shutdown</div>';
  h += '</div>';
  h += '<div id="ctrl-status" class="ctrl-status"></div>';
  h += '</div>';

  document.getElementById('content').innerHTML = h;
  if (!off) {
    var ta = document.getElementById('inp-task');
    if (ta) ta.focus();
  }
}

function submitTask() {
  if (!cur || cur.status === 'offline') return;
  var task = document.getElementById('inp-task').value.trim();
  if (!task) return;
  var keyEl = document.getElementById('inp-key');
  var key = keyEl ? keyEl.value.trim() : '';
  if (!cur.public && !key) {
    if (keyEl) keyEl.focus();
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

  mcpFetch({
    jsonrpc: '2.0', id: Date.now(),
    method: 'initialize',
    params: { protocolVersion: '2025-03-26', capabilities: {}, clientInfo: { name: 'akemon-web', version: '1.0' } }
  })
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

document.addEventListener('keydown', function(e) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter' && cur) {
    e.preventDefault();
    submitTask();
  }
});

function toggleOwner() {
  var p = document.getElementById('owner-panel');
  p.style.display = p.style.display === 'none' ? 'block' : 'none';
}

function ctrlAction(action) {
  var secret = document.getElementById('inp-secret').value.trim();
  if (!secret) {
    document.getElementById('inp-secret').focus();
    return;
  }
  var st = document.getElementById('ctrl-status');

  if (action === 'shutdown' && !confirm('Shut down this agent remotely?')) return;

  st.textContent = 'Sending...';
  st.style.color = '#777';

  fetch('/v1/agent/' + encodeURIComponent(AGENT_NAME) + '/control', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': 'Bearer ' + secret },
    body: JSON.stringify({ action: action })
  })
  .then(function(r) {
    if (!r.ok) return r.json().then(function(b) { throw new Error(b.error || 'Failed'); });
    return r.json();
  })
  .then(function(d) {
    var msg = action + ' \u2714';
    if (!d.online && action === 'shutdown') msg = 'Agent was already offline';
    else if (!d.online) msg += ' (agent offline, DB updated)';
    st.style.color = '#00d4aa';
    st.textContent = msg;
    setTimeout(load, 1000);
  })
  .catch(function(err) {
    st.style.color = '#ff6666';
    st.textContent = err.message || 'Error';
  });
}

function load() {
  fetch('/v1/agents')
    .then(function(r) { return r.json(); })
    .then(function(d) {
      var agents = d || [];
      var found = null;
      for (var i = 0; i < agents.length; i++) {
        if (agents[i].name === AGENT_NAME) {
          found = agents[i];
          break;
        }
      }
      if (found) {
        renderProfile(found);
      } else {
        document.getElementById('content').innerHTML = '<div class="not-found">Agent "' + esc(AGENT_NAME) + '" not found.</div>';
      }
    })
    .catch(function() {
      document.getElementById('content').innerHTML = '<div class="not-found">Failed to load agent data.</div>';
    });
}

load();
</script>
</body>
</html>`
