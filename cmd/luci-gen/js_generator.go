package main

func generateJS(_ *UISchema, _ map[string]*StructDef) string {
	return `'use strict';
'require view';

return view.extend({
	render: function() {
		var root = E('div', { 'class': 'tollgate-page' });

		root.innerHTML = [
			'<style>',
			'.tollgate-page{max-width:1040px}',
			'.tg-pane{display:none}',
			'.tg-pane.active{display:block}',
			'.tg-tabbar{display:flex;gap:8px;flex-wrap:wrap;margin:0 0 1.2rem 0}',
			'.tg-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));gap:12px}',
			'.tg-card{margin:0 0 1rem 0;padding:1rem;border:1px solid var(--border-color,#ddd);border-radius:4px}',
			'.tg-card h3,.tg-card h4{margin-top:0}',
			'.tg-metric-label{font-size:13px;opacity:.7;margin:0 0 .25rem 0}',
			'.tg-metric-value{font-size:28px;font-weight:700;margin:0}',
			'.tg-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:12px}',
			'.tg-muted{opacity:.7;font-size:13px}',
			'.tg-ok{color:#5cb85c}',
			'.tg-err{color:#d9534f}',
			'.tg-pre{max-height:24rem;overflow:auto;white-space:pre-wrap;font-family:monospace;font-size:13px;margin:0}',
			'.tg-editor{width:100%;min-height:18rem;font-family:monospace;font-size:12px;line-height:1.45;resize:vertical}',
			'.tg-file-state{font-size:13px;min-height:1.2rem}',
			'</style>',
			'<div class="cbi-map">',
			'<h2 name="content">TollGate</h2>',
			'<div class="cbi-map-descr">Minimal LuCI view: dashboard plus raw JSON editing for config and identities.</div>',
			'<div class="tg-tabbar">',
			'<button id="tab_dashboard" class="cbi-button cbi-button-action" type="button">Dashboard</button>',
			'<button id="tab_files" class="cbi-button" type="button">JSON files</button>',
			'</div>',
			'<div id="pane_dashboard" class="tg-pane active">',
			'<div class="tg-grid">',
			'<div class="tg-card"><p class="tg-metric-label">Wallet balance</p><p id="wallet_balance" class="tg-metric-value">—</p></div>',
			'<div class="tg-card"><p class="tg-metric-label">TollGate version</p><pre id="version_text" class="tg-pre">Loading…</pre></div>',
			'</div>',
			'<div class="tg-card"><h3>Status</h3><pre id="status_text" class="tg-pre">Loading…</pre></div>',
			'<div class="tg-card"><h3>Logs</h3><div class="tg-muted">Recent tollgate-wrt log lines. Refreshes automatically while this tab is open.</div><pre id="logs_box" class="tg-pre">Loading…</pre></div>',
			'</div>',
			'<div id="pane_files" class="tg-pane">',
			'<div class="tg-card">',
			'<h3>JSON files</h3>',
			'<div class="tg-muted">Edit the raw files directly. Accepted mints and profit share live in <code>config.json</code>. Owned and public identities are both editable in <code>identities.json</code>.</div>',
			'<div class="tg-actions"><button id="reload_files" class="cbi-button" type="button">Reload both files</button><span id="files_state" class="tg-muted"></span></div>',
			'</div>',
			'<div class="tg-card">',
			'<h3>config.json</h3>',
			'<textarea id="config_editor" class="tg-editor" spellcheck="false"></textarea>',
			'<div class="tg-actions">',
			'<button id="validate_config" class="cbi-button cbi-button-action" type="button">Validate</button>',
			'<button id="save_config" class="cbi-button cbi-button-save" type="button">Save config.json</button>',
			'<span id="config_state" class="tg-file-state tg-muted"></span>',
			'</div>',
			'</div>',
			'<div class="tg-card">',
			'<h3>identities.json</h3>',
			'<textarea id="identities_editor" class="tg-editor" spellcheck="false"></textarea>',
			'<div class="tg-actions">',
			'<button id="validate_identities" class="cbi-button cbi-button-action" type="button">Validate</button>',
			'<button id="save_identities" class="cbi-button cbi-button-save" type="button">Save identities.json</button>',
			'<span id="identities_state" class="tg-file-state tg-muted"></span>',
			'</div>',
			'</div>',
			'</div>',
			'<div class="tg-card"><h3>Messages</h3><pre id="messages" class="tg-pre">Ready.</pre></div>',
			'</div>'
		].join('');

		var API = '/cgi-bin/tollgate-api';
		var state = {
			activeTab: 'dashboard',
			configDirty: false,
			identitiesDirty: false,
			dashboardTimer: null,
			pollStarted: false
		};

		function q(sel) { return root.querySelector(sel); }
		function must(sel) { var el = q(sel); if (!el) throw new Error('Missing: ' + sel); return el; }
		function pretty(obj) { return JSON.stringify(obj, null, 2) + '\n'; }
		function parseJson(text) { return JSON.parse(text || '{}'); }
		function setMsg(lines) { must('#messages').textContent = lines.join('\n'); }
		function setState(sel, text, cls) {
			var el = must(sel);
			el.className = 'tg-file-state ' + (cls || 'tg-muted');
			el.textContent = text;
		}
		function api(action, body) {
			var opts = { method: body == null ? 'GET' : 'POST' };
			if (body != null) {
				opts.headers = { 'Content-Type': 'text/plain;charset=UTF-8' };
				opts.body = body;
			}
			return fetch(API + '?action=' + encodeURIComponent(action), opts).then(function(res) {
				return res.json();
			});
		}
		function editorValue(sel) { return must(sel).value; }
		function setEditorValue(sel, value) { must(sel).value = value || ''; }
		function markDirty(kind, dirty) {
			state[kind + 'Dirty'] = dirty;
			if (kind === 'config') {
				setState('#config_state', dirty ? 'Unsaved changes' : 'Loaded', dirty ? 'tg-err' : 'tg-muted');
			} else {
				setState('#identities_state', dirty ? 'Unsaved changes' : 'Loaded', dirty ? 'tg-err' : 'tg-muted');
			}
		}
		function formatWalletBalance(text) {
			var cleaned = String(text || '').trim();
			if (!cleaned) return '—';
			var match = cleaned.match(/(\d+)\s*sats/i);
			return match ? match[1] + ' sats' : cleaned;
		}
		function renderDashboard(data) {
			must('#wallet_balance').textContent = formatWalletBalance(data.wallet_balance);
			must('#version_text').textContent = String(data.version || '—').trim() || '—';
			must('#status_text').textContent = String(data.status || 'No status output.').trim() || 'No status output.';
			must('#logs_box').textContent = String(data.logs || 'No tollgate-wrt log lines.').trim() || 'No tollgate-wrt log lines.';
		}
		function refreshDashboard() {
			return api('dashboard').then(function(data) {
				if (!data.ok) throw new Error(data.error || 'Dashboard request failed');
				renderDashboard(data);
			}).catch(function(err) {
				setMsg(['Dashboard refresh failed: ' + String(err)]);
			});
		}
		function loadFiles(force) {
			if (!force && (state.configDirty || state.identitiesDirty) && !window.confirm('Reload both files and discard unsaved JSON changes?')) return;
			must('#files_state').textContent = 'Loading…';
			api('files').then(function(data) {
				if (!data.ok) throw new Error(data.error || 'Failed to load JSON files');
				setEditorValue('#config_editor', pretty(data.config || {}));
				setEditorValue('#identities_editor', pretty(data.identities || {}));
				markDirty('config', false);
				markDirty('identities', false);
				must('#files_state').textContent = 'Loaded ' + new Date().toLocaleTimeString();
				setMsg(['Loaded config.json and identities.json from router.']);
			}).catch(function(err) {
				must('#files_state').textContent = 'Load failed';
				setMsg(['Load failed: ' + String(err)]);
			});
		}
		function validateEditor(action, selector, label, stateSel) {
			try {
				parseJson(editorValue(selector));
			} catch (err) {
				setState(stateSel, 'Invalid JSON', 'tg-err');
				setMsg([label + ' validation failed: ' + err.message]);
				return;
			}
			setState(stateSel, 'Validating…', 'tg-muted');
			api(action, editorValue(selector)).then(function(res) {
				if (res.ok) {
					setState(stateSel, 'Valid JSON', 'tg-ok');
					setMsg([label + ' is valid JSON.']);
				} else {
					setState(stateSel, 'Invalid JSON', 'tg-err');
					setMsg([label + ' validation failed: ' + (res.error || 'Unknown error')]);
				}
			}).catch(function(err) {
				setState(stateSel, 'Validation failed', 'tg-err');
				setMsg([label + ' validation failed: ' + String(err)]);
			});
		}
		function saveEditor(action, selector, label, stateSel, kind) {
			try {
				parseJson(editorValue(selector));
			} catch (err) {
				setState(stateSel, 'Invalid JSON', 'tg-err');
				setMsg([label + ' save failed: ' + err.message]);
				return;
			}
			setState(stateSel, 'Saving…', 'tg-muted');
			api(action, editorValue(selector)).then(function(res) {
				if (!res.ok) {
					setState(stateSel, 'Save failed', 'tg-err');
					setMsg([label + ' save failed: ' + (res.error || 'Unknown error')]);
					return;
				}
				markDirty(kind, false);
				setState(stateSel, 'Saved', 'tg-ok');
				var lines = [label + ' saved.'];
				if (res.backup) lines.push('Backup: ' + res.backup);
				if (res.status) lines.push('', 'tollgate status:', res.status);
				setMsg(lines);
				refreshDashboard();
			}).catch(function(err) {
				setState(stateSel, 'Save failed', 'tg-err');
				setMsg([label + ' save failed: ' + String(err)]);
			});
		}
		function setActiveTab(name) {
			state.activeTab = name;
			must('#pane_dashboard').classList.toggle('active', name === 'dashboard');
			must('#pane_files').classList.toggle('active', name === 'files');
			must('#tab_dashboard').className = name === 'dashboard' ? 'cbi-button cbi-button-action' : 'cbi-button';
			must('#tab_files').className = name === 'files' ? 'cbi-button cbi-button-action' : 'cbi-button';
		}
		function startDashboardPolling() {
			if (state.pollStarted) return;
			state.pollStarted = true;
			if (typeof L !== 'undefined' && L.Poll && L.Poll.add) {
				L.Poll.add(function() {
					if (document.hidden || state.activeTab !== 'dashboard') return;
					return refreshDashboard();
				}, 5);
				return;
			}
			state.dashboardTimer = window.setInterval(function() {
				if (document.hidden || state.activeTab !== 'dashboard') return;
				refreshDashboard();
			}, 5000);
		}
		function bindHandlers() {
			must('#tab_dashboard').onclick = function() { setActiveTab('dashboard'); };
			must('#tab_files').onclick = function() { setActiveTab('files'); };
			must('#reload_files').onclick = function() { loadFiles(false); };
			must('#validate_config').onclick = function() { validateEditor('validate_config', '#config_editor', 'config.json', '#config_state'); };
			must('#save_config').onclick = function() { saveEditor('save_config', '#config_editor', 'config.json', '#config_state', 'config'); };
			must('#validate_identities').onclick = function() { validateEditor('validate_identities', '#identities_editor', 'identities.json', '#identities_state'); };
			must('#save_identities').onclick = function() { saveEditor('save_identities', '#identities_editor', 'identities.json', '#identities_state', 'identities'); };
			must('#config_editor').addEventListener('input', function() { markDirty('config', true); });
			must('#identities_editor').addEventListener('input', function() { markDirty('identities', true); });
		}

		try {
			bindHandlers();
			setActiveTab('dashboard');
			loadFiles(true);
			refreshDashboard();
			startDashboardPolling();
		} catch (err) {
			root.innerHTML = '<div class="cbi-map"><h2>TollGate</h2><pre>' + String(err.message || err) + '</pre></div>';
		}

		return root;
	}
});
`
}
