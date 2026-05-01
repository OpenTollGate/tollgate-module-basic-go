'use strict';
'require view';

return view.extend({
	render: function() {
		var root = E('div', { 'class': 'tollgate-page' });

		root.innerHTML = [
			'<style>',
			'.tollgate-page{max-width:1040px}',
			'.tg-pane{display:none}',
			'.tg-pane.active{display:block}',
			'.tg-tabbar{display:flex;gap:6px;flex-wrap:wrap;margin:0 0 1.2rem 0;padding:0}',
			'.tg-tabbar button{margin:0}',
			'.tg-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:12px}',
			'.tg-card{margin:0 0 1rem 0;padding:1rem;border:1px solid var(--border-color,#ddd);border-radius:4px}',
			'.tg-card h3,.tg-card h4{margin:0 0 .5rem 0}',
			'.tg-metric-label{font-size:13px;opacity:.7;margin:0 0 .25rem 0}',
			'.tg-metric-value{font-size:28px;font-weight:700;margin:0}',
			'.tg-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap;margin-top:12px}',
			'.tg-muted{opacity:.7;font-size:13px}',
			'.tg-ok{color:#5cb85c}',
			'.tg-err{color:#d9534f}',
			'.tg-warn{color:#f0ad4e}',
			'.tg-pre{max-height:24rem;overflow:auto;white-space:pre-wrap;font-family:monospace;font-size:13px;margin:0;padding:.5rem;background:var(--background-color,#f5f5f5);border-radius:3px}',
			'.tg-editor{width:100%;min-height:18rem;font-family:monospace;font-size:12px;line-height:1.45;resize:vertical}',
			'.tg-file-state{font-size:13px;min-height:1.2rem}',
			'.tg-field{margin:0 0 1rem 0}',
			'.tg-field label{display:block;font-weight:600;margin:0 0 .25rem 0;font-size:13px}',
			'.tg-field input,.tg-field select{width:100%;max-width:400px;padding:.35rem .5rem;font-size:14px}',
			'.tg-field .tg-hint{font-size:12px;opacity:.7;margin:.25rem 0 0 0}',
			'.tg-status{display:inline-block;padding:2px 8px;border-radius:3px;font-size:12px;font-weight:600}',
			'.tg-status.running{background:#dff0d8;color:#3c763d}',
			'.tg-status.stopped{background:#f2dede;color:#a94442}',
			'.tg-status.unknown{background:#fcf8e3;color:#8a6d3b}',
			'.tg-badge{display:inline-block;padding:2px 6px;border-radius:3px;font-size:11px;font-weight:600;margin-left:4px}',
			'.tg-confirm-overlay{position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.4);z-index:1000;display:flex;align-items:center;justify-content:center}',
			'.tg-confirm-box{background:#fff;padding:1.5rem;border-radius:6px;max-width:420px;width:90%}',
			'.tg-confirm-box h3{margin:0 0 .75rem 0}',
			'.tg-confirm-box p{margin:0 0 1rem 0;font-size:14px}',
			'.tg-confirm-box .tg-actions{justify-content:flex-end}',
			'.tg-password-row{display:flex;gap:6px;align-items:center}',
			'.tg-password-row input{flex:1;max-width:340px}',
			'</style>',
			'<div class="cbi-map">',
			'<h2 name="content">TollGate</h2>',
			'<div class="cbi-map-descr">Manage your TollGate captive portal payment gateway.</div>',
			'<div class="tg-tabbar">',
			'<button id="tab_overview" class="cbi-button cbi-button-action" type="button">Overview</button>',
			'<button id="tab_wallet" class="cbi-button" type="button">Wallet</button>',
			'<button id="tab_network" class="cbi-button" type="button">Network</button>',
			'<button id="tab_config" class="cbi-button" type="button">Configuration</button>',
			'<button id="tab_logs" class="cbi-button" type="button">Logs</button>',
			'<button id="tab_advanced" class="cbi-button" type="button">Advanced</button>',
			'</div>',

			'<div id="pane_overview" class="tg-pane active">',
			'<div class="cbi-section">',
			'<h3>Service Status</h3>',
			'<div class="tg-grid">',
			'<div class="tg-card"><p class="tg-metric-label">Wallet Balance</p><p id="ov_balance" class="tg-metric-value">—</p></div>',
			'<div class="tg-card"><p class="tg-metric-label">Service</p><p id="ov_service" class="tg-metric-value"><span class="tg-status unknown">Loading</span></p></div>',
			'</div>',
			'<div class="tg-actions">',
			'<button id="ov_start" class="cbi-button cbi-button-apply" type="button">Start</button>',
			'<button id="ov_stop" class="cbi-button cbi-button-remove" type="button">Stop</button>',
			'<button id="ov_restart" class="cbi-button cbi-button-save" type="button">Restart</button>',
			'</div>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Version</h3>',
			'<pre id="ov_version" class="tg-pre">Loading…</pre>',
			'</div>',
			'</div>',

			'<div id="pane_wallet" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>Wallet Balance</h3>',
			'<div class="tg-grid">',
			'<div class="tg-card"><p class="tg-metric-label">Total Balance</p><p id="wl_balance" class="tg-metric-value">—</p></div>',
			'</div>',
			'<pre id="wl_info" class="tg-pre">Loading…</pre>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Fund Wallet</h3>',
			'<div class="tg-field">',
			'<label for="wl_token_input">Cashu Token</label>',
			'<input type="password" id="wl_token_input" class="cbi-input-password" placeholder="Paste your Cashu ecash token">',
			'<div class="tg-hint">Paste a Cashu token to add funds to the wallet. The token will be consumed.</div>',
			'</div>',
			'<div class="tg-actions">',
			'<button id="wl_fund" class="cbi-button cbi-button-apply" type="button">Fund Wallet</button>',
			'<span id="wl_fund_state" class="tg-muted"></span>',
			'</div>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Drain Wallet</h3>',
			'<p class="tg-muted">Convert all wallet funds to Cashu tokens. The tokens will be displayed below — copy them to a safe place.</p>',
			'<div class="tg-actions">',
			'<button id="wl_drain" class="cbi-button cbi-button-remove" type="button">Drain All Funds</button>',
			'<span id="wl_drain_state" class="tg-muted"></span>',
			'</div>',
			'<pre id="wl_drain_result" class="tg-pre" style="display:none"></pre>',
			'</div>',
			'</div>',

			'<div id="pane_network" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>Private WiFi Network</h3>',
			'<div id="nw_status_loading" class="tg-muted">Loading…</div>',
			'<div id="nw_status_content" style="display:none">',
			'<div class="tg-grid">',
			'<div class="tg-card"><p class="tg-metric-label">Status</p><p id="nw_enabled" class="tg-metric-value"><span class="tg-status unknown">—</span></p></div>',
			'<div class="tg-card"><p class="tg-metric-label">SSID</p><p id="nw_ssid" class="tg-metric-value" style="font-size:20px">—</p></div>',
			'<div class="tg-card"><p class="tg-metric-label">Password</p><p id="nw_password" class="tg-metric-value" style="font-size:16px">—</p></div>',
			'</div>',
			'<div class="tg-actions">',
			'<button id="nw_enable" class="cbi-button cbi-button-apply" type="button">Enable</button>',
			'<button id="nw_disable" class="cbi-button cbi-button-remove" type="button">Disable</button>',
			'</div>',
			'</div>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Rename Network</h3>',
			'<div class="tg-field">',
			'<label for="nw_new_ssid">New SSID</label>',
			'<input type="text" id="nw_new_ssid" class="cbi-input-text" placeholder="Enter new network name">',
			'</div>',
			'<div class="tg-actions">',
			'<button id="nw_rename" class="cbi-button cbi-button-save" type="button">Rename</button>',
			'<span id="nw_rename_state" class="tg-muted"></span>',
			'</div>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Change Password</h3>',
			'<div class="tg-field">',
			'<label for="nw_new_pw">New Password</label>',
			'<div class="tg-password-row">',
			'<input type="password" id="nw_new_pw" class="cbi-input-password" placeholder="Leave empty to generate random">',
			'</div>',
			'<div class="tg-hint">Leave empty to auto-generate a memorable password.</div>',
			'</div>',
			'<div class="tg-actions">',
			'<button id="nw_setpw" class="cbi-button cbi-button-save" type="button">Change Password</button>',
			'<span id="nw_pw_state" class="tg-muted"></span>',
			'</div>',
			'</div>',
			'</div>',

			'<div id="pane_config" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>Pricing</h3>',
			'<div id="cfg_loading" class="tg-muted">Loading configuration…</div>',
			'<div id="cfg_content" style="display:none">',
			'<div class="tg-grid">',
			'<div class="tg-card"><p class="tg-metric-label">Price per Step</p><p id="cfg_price" class="tg-metric-value">—</p></div>',
			'<div class="tg-card"><p class="tg-metric-label">Step Size</p><p id="cfg_step" class="tg-metric-value">—</p></div>',
			'<div class="tg-card"><p class="tg-metric-label">Metric</p><p id="cfg_metric" class="tg-metric-value">—</p></div>',
			'</div>',
			'</div>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Accepted Mints</h3>',
			'<pre id="cfg_mints" class="tg-pre">Loading…</pre>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Profit Share</h3>',
			'<pre id="cfg_profit" class="tg-pre">Loading…</pre>',
			'</div>',
			'</div>',

			'<div id="pane_logs" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>Service Logs</h3>',
			'<div class="tg-muted">Recent tollgate-wrt log lines. Auto-refreshes while this tab is open.</div>',
			'<pre id="logs_box" class="tg-pre">Loading…</pre>',
			'</div>',
			'</div>',

			'<div id="pane_advanced" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>Raw JSON Editor</h3>',
			'<div class="tg-muted">Edit configuration files directly. Changes here take effect after saving. Use with caution.</div>',
			'<div class="tg-actions"><button id="reload_files" class="cbi-button" type="button">Reload both files</button><span id="files_state" class="tg-muted"></span></div>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>config.json</h3>',
			'<textarea id="config_editor" class="tg-editor" spellcheck="false"></textarea>',
			'<div class="tg-actions">',
			'<button id="validate_config" class="cbi-button cbi-button-action" type="button">Validate</button>',
			'<button id="save_config" class="cbi-button cbi-button-save" type="button">Save config.json</button>',
			'<span id="config_state" class="tg-file-state tg-muted"></span>',
			'</div>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>identities.json</h3>',
			'<textarea id="identities_editor" class="tg-editor" spellcheck="false"></textarea>',
			'<div class="tg-actions">',
			'<button id="validate_identities" class="cbi-button cbi-button-action" type="button">Validate</button>',
			'<button id="save_identities" class="cbi-button cbi-button-save" type="button">Save identities.json</button>',
			'<span id="identities_state" class="tg-file-state tg-muted"></span>',
			'</div>',
			'</div>',
			'</div>',

			'<div class="cbi-section"><pre id="messages" class="tg-pre" style="display:none"></pre></div>',
			'</div>'
		].join('');

		var API = '/cgi-bin/tollgate-api';
		var state = {
			activeTab: 'overview',
			configDirty: false,
			identitiesDirty: false,
			pollStarted: false
		};

		function q(sel) { return root.querySelector(sel); }
		function must(sel) { var el = q(sel); if (!el) throw new Error('Missing: ' + sel); return el; }
		function pretty(obj) { return JSON.stringify(obj, null, 2) + '\n'; }
		function parseJson(text) { try { return JSON.parse(text || '{}'); } catch(e) { return null; } }
		function setMsg(text) { var el = must('#messages'); el.textContent = text; el.style.display = text ? 'block' : 'none'; }

		function api(action, body) {
			var opts = { method: body == null ? 'GET' : 'POST' };
			if (body != null) {
				opts.headers = { 'Content-Type': 'application/json;charset=UTF-8' };
				opts.body = typeof body === 'string' ? body : JSON.stringify(body);
			}
			return fetch(API + '?action=' + encodeURIComponent(action), opts).then(function(res) {
				return res.json();
			});
		}

		function confirmAction(title, message, onConfirm) {
			var overlay = document.createElement('div');
			overlay.className = 'tg-confirm-overlay';
			overlay.innerHTML = '<div class="tg-confirm-box"><h3>' + title + '</h3><p>' + message + '</p><div class="tg-actions"><button class="cbi-button cbi-button-remove confirm-yes">Confirm</button><button class="cbi-button confirm-no">Cancel</button></div></div>';
			document.body.appendChild(overlay);
			overlay.querySelector('.confirm-yes').onclick = function() { document.body.removeChild(overlay); onConfirm(); };
			overlay.querySelector('.confirm-no').onclick = function() { document.body.removeChild(overlay); };
			overlay.onclick = function(e) { if (e.target === overlay) document.body.removeChild(overlay); };
		}

		function formatBalance(text) {
			var cleaned = String(text || '').trim();
			if (!cleaned) return '—';
			var match = cleaned.match(/(\d+)\s*sats/i);
			return match ? match[1] + ' sats' : cleaned;
		}

		function extractNumber(text) {
			var match = String(text || '').match(/(\d+)/);
			return match ? parseInt(match[1], 10) : null;
		}

		function humanStepSize(bytes) {
			if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GiB';
			if (bytes >= 1048576) return (bytes / 1048576).toFixed(1) + ' MiB';
			if (bytes >= 1024) return (bytes / 1024).toFixed(1) + ' KiB';
			return bytes + ' B';
		}

		function renderServiceStatus(statusText) {
			var el = must('#ov_service');
			var running = /running|active|uptime/i.test(statusText || '');
			var stopped = /stopped|not running|inactive/i.test(statusText || '');
			if (running) {
				el.innerHTML = '<span class="tg-status running">Running</span>';
			} else if (stopped) {
				el.innerHTML = '<span class="tg-status stopped">Stopped</span>';
			} else {
				el.innerHTML = '<span class="tg-status unknown">' + (statusText ? 'Unknown' : 'No status') + '</span>';
			}
		}

		function refreshOverview() {
			return api('dashboard').then(function(data) {
				if (!data.ok) throw new Error(data.error || 'Dashboard failed');
				must('#ov_balance').textContent = formatBalance(data.wallet_balance);
				must('#ov_version').textContent = String(data.version || '—').trim();
				renderServiceStatus(data.status);
				must('#logs_box').textContent = String(data.logs || 'No log lines.').trim();
			}).catch(function(err) {
				setMsg('Dashboard refresh failed: ' + err);
			});
		}

		function refreshWallet() {
			api('dashboard').then(function(data) {
				if (data.ok) must('#wl_balance').textContent = formatBalance(data.wallet_balance);
			});
			return api('wallet_info').then(function(data) {
				if (!data.ok) throw new Error(data.error || 'Wallet info failed');
				must('#wl_info').textContent = String(data.info || 'No wallet info.').trim();
			}).catch(function(err) {
				must('#wl_info').textContent = 'Failed to load wallet info: ' + err;
			});
		}

		function refreshNetwork() {
			must('#nw_status_loading').style.display = 'block';
			must('#nw_status_content').style.display = 'none';
			return api('wifi_status').then(function(data) {
				var resp = data.response ? parseJson(data.response) : null;
				var d = resp && resp.data ? resp.data : null;
				if (!d) throw new Error('No network data');

				var enabledEl = must('#nw_enabled');
				if (d.enabled) {
					enabledEl.innerHTML = '<span class="tg-status running">Enabled</span>';
				} else {
					enabledEl.innerHTML = '<span class="tg-status stopped">Disabled</span>';
				}

				must('#nw_ssid').textContent = d.ssid || '—';
				var pw = d.password || '—';
				must('#nw_password').textContent = pw.length > 20 ? pw.substring(0, 20) + '…' : pw;

				must('#nw_status_loading').style.display = 'none';
				must('#nw_status_content').style.display = 'block';
			}).catch(function(err) {
				must('#nw_status_loading').textContent = 'Failed to load: ' + err;
			});
		}

		function refreshConfig() {
			return api('files').then(function(data) {
				if (!data.ok) throw new Error(data.error || 'Failed to load config');

				var config = data.config || {};
				var mints = config.accepted_mints || [];
				var profit = config.profit_share || [];

				if (mints.length > 0) {
					var m = mints[0];
					must('#cfg_price').textContent = (m.price_per_step || '—') + ' ' + (m.price_unit || 'sats');
					must('#cfg_step').textContent = config.metric === 'milliseconds'
						? (config.step_size / 1000) + 's'
						: humanStepSize(config.step_size || 0);
					must('#cfg_metric').textContent = config.metric || '—';
				} else {
					must('#cfg_price').textContent = '—';
					must('#cfg_step').textContent = '—';
					must('#cfg_metric').textContent = '—';
				}

				must('#cfg_mints').textContent = JSON.stringify(mints, null, 2);
				must('#cfg_profit').textContent = JSON.stringify(profit, null, 2);

				must('#cfg_loading').style.display = 'none';
				must('#cfg_content').style.display = 'block';
			}).catch(function(err) {
				must('#cfg_loading').textContent = 'Failed: ' + err;
			});
		}

		function loadFiles(force) {
			if (!force && (state.configDirty || state.identitiesDirty) && !window.confirm('Reload and discard unsaved changes?')) return;
			must('#files_state').textContent = 'Loading…';
			api('files').then(function(data) {
				if (!data.ok) throw new Error(data.error || 'Failed to load');
				must('#config_editor').value = pretty(data.config || {});
				must('#identities_editor').value = pretty(data.identities || {});
				state.configDirty = false;
				state.identitiesDirty = false;
				must('#config_state').textContent = '';
				must('#identities_state').textContent = '';
				must('#files_state').textContent = 'Loaded ' + new Date().toLocaleTimeString();
			}).catch(function(err) {
				must('#files_state').textContent = 'Load failed: ' + err;
			});
		}

		function validateEditor(action, selector, label, stateSel) {
			var text = must(selector).value;
			try { JSON.parse(text); } catch(e) {
				must(stateSel).textContent = 'Invalid JSON';
				must(stateSel).className = 'tg-file-state tg-err';
				return;
			}
			must(stateSel).textContent = 'Validating…';
			api(action, text).then(function(res) {
				if (res.ok) {
					must(stateSel).textContent = 'Valid JSON';
					must(stateSel).className = 'tg-file-state tg-ok';
				} else {
					must(stateSel).textContent = 'Invalid: ' + (res.error || '');
					must(stateSel).className = 'tg-file-state tg-err';
				}
			}).catch(function(err) {
				must(stateSel).textContent = 'Error: ' + err;
				must(stateSel).className = 'tg-file-state tg-err';
			});
		}

		function saveEditor(action, selector, label, stateSel, kind) {
			var text = must(selector).value;
			try { JSON.parse(text); } catch(e) {
				must(stateSel).textContent = 'Invalid JSON';
				must(stateSel).className = 'tg-file-state tg-err';
				return;
			}
			must(stateSel).textContent = 'Saving…';
			api(action, text).then(function(res) {
				if (!res.ok) {
					must(stateSel).textContent = 'Save failed: ' + (res.error || '');
					must(stateSel).className = 'tg-file-state tg-err';
					return;
				}
				state[kind + 'Dirty'] = false;
				must(stateSel).textContent = 'Saved';
				must(stateSel).className = 'tg-file-state tg-ok';
				refreshOverview();
				refreshConfig();
			}).catch(function(err) {
				must(stateSel).textContent = 'Error: ' + err;
				must(stateSel).className = 'tg-file-state tg-err';
			});
		}

		function setActiveTab(name) {
			state.activeTab = name;
			var tabs = ['overview', 'wallet', 'network', 'config', 'logs', 'advanced'];
			tabs.forEach(function(t) {
				var pane = must('#pane_' + t);
				var btn = must('#tab_' + t);
				pane.classList.toggle('active', t === name);
				btn.className = t === name ? 'cbi-button cbi-button-action' : 'cbi-button';
			});
			if (name === 'wallet') refreshWallet();
			if (name === 'network') refreshNetwork();
			if (name === 'config') refreshConfig();
			if (name === 'advanced') loadFiles(true);
		}

		function startPolling() {
			if (state.pollStarted) return;
			state.pollStarted = true;
			if (typeof L !== 'undefined' && L.Poll && L.Poll.add) {
				L.Poll.add(function() {
					if (document.hidden) return;
					if (state.activeTab === 'overview') return refreshOverview();
					if (state.activeTab === 'logs') {
						return api('dashboard').then(function(data) {
							if (data.ok) must('#logs_box').textContent = String(data.logs || '').trim();
						});
					}
				}, 5);
				return;
			}
			setInterval(function() {
				if (document.hidden) return;
				if (state.activeTab === 'overview') refreshOverview();
				if (state.activeTab === 'logs') {
					api('dashboard').then(function(data) {
						if (data.ok) must('#logs_box').textContent = String(data.logs || '').trim();
					});
				}
			}, 5000);
		}

		function bindHandlers() {
			var tabs = ['overview', 'wallet', 'network', 'config', 'logs', 'advanced'];
			tabs.forEach(function(t) {
				must('#tab_' + t).onclick = function() { setActiveTab(t); };
			});

			must('#ov_start').onclick = function() {
				must('#ov_start').disabled = true;
				api('service_start').then(function() { refreshOverview(); }).finally(function() { must('#ov_start').disabled = false; });
			};
			must('#ov_stop').onclick = function() {
				confirmAction('Stop Services', 'This will stop TollGate and NoDogSplash. Users will lose connectivity.', function() {
					api('service_stop').then(function() { refreshOverview(); });
				});
			};
			must('#ov_restart').onclick = function() {
				must('#ov_restart').disabled = true;
				api('service_restart').then(function() { refreshOverview(); }).finally(function() { must('#ov_restart').disabled = false; });
			};

			must('#wl_fund').onclick = function() {
				var token = must('#wl_token_input').value.trim();
				if (!token) { must('#wl_fund_state').textContent = 'Enter a token first.'; return; }
				must('#wl_fund_state').textContent = 'Funding…';
				must('#wl_fund').disabled = true;
				api('wallet_fund', JSON.stringify({ token: token })).then(function(data) {
					var resp = parseJson(data.response || '{}');
					if (resp && resp.success) {
						must('#wl_fund_state').textContent = 'Funded successfully.';
						must('#wl_token_input').value = '';
						refreshWallet();
					} else {
						must('#wl_fund_state').textContent = 'Failed: ' + ((resp && resp.error) || data.error || 'Unknown error');
					}
				}).catch(function(err) {
					must('#wl_fund_state').textContent = 'Error: ' + err;
				}).finally(function() { must('#wl_fund').disabled = false; });
			};

			must('#wl_drain').onclick = function() {
				confirmAction('Drain Wallet', 'This will convert ALL wallet funds to Cashu tokens. Make sure to copy the tokens — they will no longer be in the wallet.', function() {
					must('#wl_drain_state').textContent = 'Draining…';
					must('#wl_drain').disabled = true;
					api('wallet_drain').then(function(data) {
						var resp = parseJson(data.response || '{}');
						if (resp && resp.success) {
							must('#wl_drain_state').textContent = 'Drained successfully.';
							var d = resp.data || {};
							var tokens = d.tokens || [];
							var lines = ['Total drained: ' + (d.total_sats || 0) + ' sats', ''];
							tokens.forEach(function(t, i) {
								lines.push('Token ' + (i + 1) + ': ' + (t.balance_sats || 0) + ' sats (' + (t.mint_url || '') + ')');
								if (t.token) lines.push(t.token);
								lines.push('');
							});
							must('#wl_drain_result').textContent = lines.join('\n');
							must('#wl_drain_result').style.display = 'block';
							refreshWallet();
						} else {
							must('#wl_drain_state').textContent = 'Failed: ' + ((resp && resp.error) || data.error || 'Unknown error');
						}
					}).catch(function(err) {
						must('#wl_drain_state').textContent = 'Error: ' + err;
					}).finally(function() { must('#wl_drain').disabled = false; });
				});
			};

			must('#nw_enable').onclick = function() {
				must('#nw_enable').disabled = true;
				api('wifi_enable').then(function() { refreshNetwork(); }).finally(function() { must('#nw_enable').disabled = false; });
			};
			must('#nw_disable').onclick = function() {
				confirmAction('Disable Private WiFi', 'Disabling the private network may lock you out of the router. Make sure you have another way to access it.', function() {
					api('wifi_disable').then(function() { refreshNetwork(); });
				});
			};
			must('#nw_rename').onclick = function() {
				var ssid = must('#nw_new_ssid').value.trim();
				if (!ssid) { must('#nw_rename_state').textContent = 'Enter a new SSID.'; return; }
				must('#nw_rename_state').textContent = 'Renaming…';
				api('wifi_rename', JSON.stringify({ ssid: ssid })).then(function(data) {
					var resp = parseJson(data.response || '{}');
					if (resp && resp.success) {
						must('#nw_rename_state').textContent = 'Renamed to ' + ssid;
						must('#nw_new_ssid').value = '';
						refreshNetwork();
					} else {
						must('#nw_rename_state').textContent = 'Failed: ' + ((resp && resp.error) || 'Unknown error');
					}
				}).catch(function(err) {
					must('#nw_rename_state').textContent = 'Error: ' + err;
				});
			};
			must('#nw_setpw').onclick = function() {
				var pw = must('#nw_new_pw').value;
				must('#nw_pw_state').textContent = 'Changing…';
				api('wifi_password', JSON.stringify({ password: pw })).then(function(data) {
					var resp = parseJson(data.response || '{}');
					if (resp && resp.success) {
						must('#nw_pw_state').textContent = 'Password changed.';
						must('#nw_new_pw').value = '';
						refreshNetwork();
					} else {
						must('#nw_pw_state').textContent = 'Failed: ' + ((resp && resp.error) || 'Unknown error');
					}
				}).catch(function(err) {
					must('#nw_pw_state').textContent = 'Error: ' + err;
				});
			};

			must('#reload_files').onclick = function() { loadFiles(false); };
			must('#validate_config').onclick = function() { validateEditor('validate_config', '#config_editor', 'config.json', '#config_state'); };
			must('#save_config').onclick = function() { saveEditor('save_config', '#config_editor', 'config.json', '#config_state', 'config'); };
			must('#validate_identities').onclick = function() { validateEditor('validate_identities', '#identities_editor', 'identities.json', '#identities_state'); };
			must('#save_identities').onclick = function() { saveEditor('save_identities', '#identities_editor', 'identities.json', '#identities_state', 'identities'); };
			must('#config_editor').addEventListener('input', function() { state.configDirty = true; must('#config_state').textContent = 'Unsaved changes'; must('#config_state').className = 'tg-file-state tg-err'; });
			must('#identities_editor').addEventListener('input', function() { state.identitiesDirty = true; must('#identities_state').textContent = 'Unsaved changes'; must('#identities_state').className = 'tg-file-state tg-err'; });
		}

		try {
			bindHandlers();
			setActiveTab('overview');
			refreshOverview();
			startPolling();
		} catch (err) {
			root.innerHTML = '<div class="cbi-map"><h2>TollGate</h2><pre>' + String(err.message || err) + '</pre></div>';
		}

		return root;
	}
});
