'use strict';
'require view';

return view.extend({
	render: function() {
		var root = E('div', { 'class': 'tollgate-live-page' });

		root.innerHTML = [
			'<style>',
			'.tollgate-live-page{max-width:1200px}',
			'.tg-pane{display:none}',
			'.tg-pane.active{display:block}',
			'.tg-tabbar{display:flex;gap:8px;flex-wrap:wrap;margin:0 0 1rem 0}',
			'.tg-form-grid{display:grid;grid-template-columns:220px minmax(0,1fr);gap:10px 16px;align-items:center}',
			'.tg-form-grid label{font-weight:600}',
			'.tg-form-grid input,.tg-form-grid select,.tg-form-grid textarea{width:100%}',
			'.tg-actions{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin-top:12px}',
			'.tg-section-node{margin:0 0 1rem 0;padding:1rem;border:1px solid #ddd}',
			'.tg-logbox{max-height:24rem;overflow:auto;white-space:pre-wrap;font-family:monospace}',
			'#raw_json{width:100%;min-height:26rem;font-family:monospace}',
			'#validation_box{min-height:8rem;white-space:pre-wrap;font-family:monospace}',
			'.tg-muted{opacity:.8}',
			'</style>',
			'<div class="cbi-map">',
			'<h2 name="content">TollGate</h2>',
			'<div class="cbi-map-descr">Live TollGate overview and direct editor for <code>/etc/tollgate/config.json</code>.</div>',
			'<div class="tg-tabbar">',
			'<button id="tab_overview" class="cbi-button cbi-button-action" type="button">Overview</button>',
			'<button id="tab_form" class="cbi-button" type="button">Configuration</button>',
			'<button id="tab_json" class="cbi-button" type="button">Raw JSON</button>',
			'<button id="tab_logs" class="cbi-button" type="button">Logs</button>',
			'</div>',

			'<div id="pane_overview" class="tg-pane active">',
			'<div class="cbi-section">',
			'<h3>Live overview</h3>',
			'<div class="cbi-section-descr">Status and wallet information are read from the live TollGate CLI.</div>',
			'<div class="tg-actions"><button id="refresh_all" class="cbi-button cbi-button-action" type="button">Refresh live data</button></div>',
			'<table class="table">',
			'<tr><th>Wallet balance</th><td id="wallet_balance">Loading…</td><th>Version</th><td id="version_text">Loading…</td></tr>',
			'<tr><th>Running</th><td id="running_state">Loading…</td><th>Config OK</th><td id="config_ok_state">Loading…</td></tr>',
			'<tr><th>Wallet OK</th><td id="wallet_ok_state">Loading…</td><th>Network OK</th><td id="network_ok_state">Loading…</td></tr>',
			'</table>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Mint wallet balances</h3>',
			'<div class="cbi-section-descr">Configured accepted mints merged with live wallet balances. Missing mint balances are shown as 0 sats.</div>',
			'<table class="table">',
			'<thead><tr><th>Mint</th><th>Balance</th></tr></thead>',
			'<tbody id="wallet_mint_rows"><tr><td colspan="2">Loading…</td></tr></tbody>',
			'</table>',
			'</div>',
			'<div class="cbi-section">',
			'<h3>Raw CLI output</h3>',
			'<details><summary>Show CLI details</summary>',
			'<pre id="raw_wallet"></pre>',
			'<pre id="raw_wallet_info"></pre>',
			'<pre id="raw_version"></pre>',
			'<pre id="raw_status"></pre>',
			'</details>',
			'</div>',
			'</div>',

			'<div id="pane_form" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>General</h3>',
			'<div class="tg-form-grid tg-section-node">',
			'<label>Log level</label><select id="log_level"><option value="debug">debug</option><option value="info">info</option><option value="warn">warn</option><option value="error">error</option></select>',
			'<label>Show setup</label><input id="show_setup" type="checkbox">',
			'<label>Reseller mode</label><input id="reseller_mode" type="checkbox">',
			'<label>Metric</label><select id="metric"><option value="milliseconds">milliseconds</option><option value="bytes">bytes</option></select>',
			'<label>Step size</label><input id="step_size" type="number">',
			'<label>Margin</label><input id="margin" type="number" step="0.000001">',
			'</div>',
			'</div>',
			'<div class="cbi-section"><h3>Accepted mints</h3><div id="mints"></div><div class="tg-actions"><button id="add_mint" class="cbi-button cbi-button-add" type="button">Add mint</button></div></div>',
			'<div class="cbi-section"><h3>Profit share</h3><div id="shares"></div><div class="tg-actions"><button id="add_share" class="cbi-button cbi-button-add" type="button">Add share</button><span>Total: <strong id="share_total">0%</strong></span></div></div>',
			'<div class="cbi-section"><h3>Relays</h3><div class="tg-section-node"><textarea id="relays" rows="5"></textarea></div></div>',
			'<div class="cbi-section">',
			'<h3>Crowsnest</h3>',
			'<div class="tg-form-grid tg-section-node">',
			'<label>Probe timeout (s)</label><input id="probe_timeout_s" type="number">',
			'<label>Probe retry count</label><input id="probe_retry_count" type="number">',
			'<label>Probe retry delay (s)</label><input id="probe_retry_delay_s" type="number">',
			'<label>Require valid signature</label><input id="require_valid_signature" type="checkbox">',
			'<label>Ignore interfaces</label><textarea id="ignore_interfaces" rows="4"></textarea>',
			'<label>Discovery timeout (s)</label><input id="discovery_timeout_s" type="number">',
			'</div>',
			'</div>',
			'<div class="cbi-section"><h3>Actions</h3><div class="tg-actions"><button id="forms_to_json" class="cbi-button cbi-button-action" type="button">Forms → JSON</button><button id="validate_form" class="cbi-button cbi-button-action" type="button">Validate form</button><button id="save_form" class="cbi-button cbi-button-save" type="button">Save form to config.json</button></div></div>',
			'</div>',

			'<div id="pane_json" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>Raw JSON editor</h3>',
			'<div class="cbi-section-descr">Use this when you want to inspect or edit the full JSON config directly.</div>',
			'<div class="tg-section-node"><textarea id="raw_json"></textarea></div>',
			'<div class="tg-actions"><button id="json_to_forms" class="cbi-button cbi-button-action" type="button">JSON → Forms</button><button id="validate_json" class="cbi-button cbi-button-action" type="button">Validate raw JSON</button><button id="save_json" class="cbi-button cbi-button-save" type="button">Save raw JSON</button></div>',
			'</div>',
			'</div>',

			'<div id="pane_logs" class="tg-pane">',
			'<div class="cbi-section">',
			'<h3>TollGate logs</h3>',
			'<div class="cbi-section-descr">Auto-refreshes every 5 seconds while this tab is open. Showing <code>daemon.err tollgate-wrt</code> lines with the syslog prefix removed.</div>',
			'<div class="tg-actions"><span id="logs_poll_state" class="tg-muted">Waiting for first update…</span></div>',
			'<pre id="logs_box" class="tg-logbox tg-section-node">Loading…</pre>',
			'</div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Validation output</h3>',
			'<pre id="validation_box" class="tg-section-node">Loading…</pre>',
			'</div>',
			'</div>'
		].join('');

		var API = '/cgi-bin/tollgate-api';
		var state = { cfg: {}, logTimer: null, activeTab: 'overview' };

		function q(sel) { return root.querySelector(sel); }
		function qa(sel) { return Array.prototype.slice.call(root.querySelectorAll(sel)); }
		function must(sel) { var el = q(sel); if (!el) throw new Error('Missing UI element: ' + sel); return el; }
		function num(v, d) { var n = Number(v); return isNaN(n) ? d : n; }
		function pretty(obj) { return JSON.stringify(obj, null, 2) + '\n'; }
		function parseJson(text) { return JSON.parse(text || '{}'); }
		function lines(v) { return String(v || '').split(/\r?\n/).map(function(x){ return x.trim(); }).filter(Boolean); }
		function html(v) {
			return String(v == null ? '' : v)
				.replace(/&/g, '&amp;')
				.replace(/</g, '&lt;')
				.replace(/>/g, '&gt;')
				.replace(/"/g, '&quot;')
				.replace(/'/g, '&#39;');
		}
		function keyvals(txt) {
			var out = {};
			String(txt || '').split(/\r?\n/).forEach(function(line) {
				var m = line.match(/^([A-Za-z0-9_]+):\s*(.+)$/);
				if (m) out[m[1]] = m[2];
			});
			return out;
		}
		function boolText(v) {
			if (v === 'true') return 'Yes';
			if (v === 'false') return 'No';
			return v || 'Unknown';
		}
		function setValidation(lines) { must('#validation_box').textContent = lines.join('\n'); }
		function walletDisplay(txt) {
			var kv = keyvals(txt), m;
			if (kv.balance_sats) return kv.balance_sats + ' sats';
			m = String(txt || '').match(/Total wallet balance:\s*(\d+)\s*sats/i);
			if (m) return m[1] + ' sats';
			return txt ? 'Could not parse' : 'No CLI output';
		}
		function parseWalletInfo(text) {
			var kv = keyvals(text);
			var out = { total: num(kv.total_balance, 0), mintCount: num(kv.mint_count, 0), balances: {} };
			String(text || '').split(/\r?\n/).forEach(function(line) {
				var m = line.match(/^\s+(.+):\s+(\d+)\s*$/);
				if (m) out.balances[m[1]] = num(m[2], 0);
			});
			return out;
		}
		function renderLogs(text) {
			var box = must('#logs_box');
			var pinned = (box.scrollTop + box.clientHeight) >= (box.scrollHeight - 24);
			box.textContent = text || 'No daemon.err tollgate-wrt entries.';
			if (pinned) box.scrollTop = box.scrollHeight;
		}
		function updateLogsStatus(label) {
			must('#logs_poll_state').textContent = label;
		}
		function renderWalletMints(cfg, walletInfoText) {
			var tbody = must('#wallet_mint_rows');
			var info = parseWalletInfo(walletInfoText);
			var mints = Array.isArray(cfg.accepted_mints) ? cfg.accepted_mints : [];
			if (!mints.length) {
				tbody.innerHTML = '<tr><td colspan="2">No accepted mints configured.</td></tr>';
				return;
			}
			tbody.innerHTML = mints.map(function(mint) {
				var url = mint && mint.url ? mint.url : '';
				var bal = Object.prototype.hasOwnProperty.call(info.balances, url) ? info.balances[url] : 0;
				return '<tr><td>' + html(url || '(missing URL)') + '</td><td>' + html(String(bal)) + ' sats</td></tr>';
			}).join('');
		}
		function mintCard(m, idx) {
			m = m || {};
			return '<div class="cbi-section-node mint-card tg-section-node"><h4>Mint #' + (idx + 1) + '</h4><div class="tg-form-grid">' +
			'<label>Mint URL</label><input class="mint-url" value="' + html(m.url || '') + '">' +
			'<label>Min balance</label><input class="mint-min-balance" type="number" value="' + html(m.min_balance || 64) + '">' +
			'<label>Balance tolerance %</label><input class="mint-balance-tolerance" type="number" value="' + html(m.balance_tolerance_percent || 10) + '">' +
			'<label>Payout interval seconds</label><input class="mint-payout-interval" type="number" value="' + html(m.payout_interval_seconds || 60) + '">' +
			'<label>Min payout amount</label><input class="mint-min-payout" type="number" value="' + html(m.min_payout_amount || 128) + '">' +
			'<label>Price per step</label><input class="mint-price-per-step" type="number" step="any" value="' + html(m.price_per_step || 1) + '">' +
			'<label>Price unit</label><input class="mint-price-unit" value="' + html(m.price_unit || 'sats') + '">' +
			'<label>Purchase min steps</label><input class="mint-purchase-min-steps" type="number" value="' + html(m.purchase_min_steps || 0) + '">' +
			'</div><div class="tg-actions"><button class="cbi-button cbi-button-remove remove-mint" type="button">Remove mint</button></div></div>';
		}
		function shareCard(s, idx) {
			s = s || {};
			var pct = (typeof s.factor === 'number') ? (s.factor * 100) : 0;
			return '<div class="cbi-section-node share-card tg-section-node"><h4>Share #' + (idx + 1) + '</h4><div class="tg-form-grid">' +
			'<label>Identity</label><input class="share-identity" value="' + html(s.identity || '') + '">' +
			'<label>Percent</label><input class="share-percent" type="number" step="0.01" value="' + html(pct) + '">' +
			'</div><div class="tg-actions"><button class="cbi-button cbi-button-remove remove-share" type="button">Remove share</button></div></div>';
		}
		function updateShareTotal() {
			var total = 0;
			qa('.share-percent').forEach(function(el){ total += num(el.value, 0); });
			must('#share_total').textContent = total.toFixed(2) + '%';
		}
		function renderMints(list) {
			var wrap = must('#mints');
			wrap.innerHTML = '';
			if (!Array.isArray(list) || !list.length) list = [ {} ];
			list.forEach(function(m, idx){ wrap.insertAdjacentHTML('beforeend', mintCard(m, idx)); });
			qa('.remove-mint').forEach(function(btn){ btn.onclick = function(){ var card = btn.closest('.mint-card'); if (card) card.remove(); }; });
		}
		function renderShares(list) {
			var wrap = must('#shares');
			wrap.innerHTML = '';
			if (!Array.isArray(list) || !list.length) list = [ {} ];
			list.forEach(function(s, idx){ wrap.insertAdjacentHTML('beforeend', shareCard(s, idx)); });
			qa('.remove-share').forEach(function(btn){ btn.onclick = function(){ var card = btn.closest('.share-card'); if (card) card.remove(); updateShareTotal(); }; });
			qa('.share-percent').forEach(function(el){ el.oninput = updateShareTotal; });
			updateShareTotal();
		}
		function populateForm(cfg) {
			cfg = cfg || {};
			cfg.accepted_mints = Array.isArray(cfg.accepted_mints) ? cfg.accepted_mints : [];
			cfg.profit_share = Array.isArray(cfg.profit_share) ? cfg.profit_share : [];
			cfg.relays = Array.isArray(cfg.relays) ? cfg.relays : [];
			cfg.crowsnest = cfg.crowsnest || {};
			must('#log_level').value = cfg.log_level || 'info';
			must('#show_setup').checked = !!cfg.show_setup;
			must('#reseller_mode').checked = !!cfg.reseller_mode;
			must('#metric').value = cfg.metric || 'milliseconds';
			must('#step_size').value = cfg.step_size || 0;
			must('#margin').value = cfg.margin || 0;
			must('#relays').value = cfg.relays.join('\n');
			must('#probe_timeout_s').value = Math.round(num(cfg.crowsnest.probe_timeout, 0) / 1000000000);
			must('#probe_retry_count').value = num(cfg.crowsnest.probe_retry_count, 0);
			must('#probe_retry_delay_s').value = Math.round(num(cfg.crowsnest.probe_retry_delay, 0) / 1000000000);
			must('#require_valid_signature').checked = !!cfg.crowsnest.require_valid_signature;
			must('#ignore_interfaces').value = (cfg.crowsnest.ignore_interfaces || []).join('\n');
			must('#discovery_timeout_s').value = Math.round(num(cfg.crowsnest.discovery_timeout, 0) / 1000000000);
			renderMints(cfg.accepted_mints);
			renderShares(cfg.profit_share);
			must('#raw_json').value = pretty(cfg);
		}
		function collectMints() {
			return qa('.mint-card').map(function(card){
				return {
					url: card.querySelector('.mint-url').value.trim(),
					min_balance: num(card.querySelector('.mint-min-balance').value, 64),
					balance_tolerance_percent: num(card.querySelector('.mint-balance-tolerance').value, 10),
					payout_interval_seconds: num(card.querySelector('.mint-payout-interval').value, 60),
					min_payout_amount: num(card.querySelector('.mint-min-payout').value, 128),
					price_per_step: num(card.querySelector('.mint-price-per-step').value, 1),
					price_unit: card.querySelector('.mint-price-unit').value.trim() || 'sats',
					purchase_min_steps: num(card.querySelector('.mint-purchase-min-steps').value, 0)
				};
			}).filter(function(m){ return m.url.length > 0; });
		}
		function collectShares() {
			return qa('.share-card').map(function(card){
				var pct = num(card.querySelector('.share-percent').value, 0);
				return { identity: card.querySelector('.share-identity').value.trim(), factor: Number((pct / 100).toFixed(8)) };
			}).filter(function(s){ return s.identity.length > 0; });
		}
		function formToObject() {
			var next = JSON.parse(JSON.stringify(state.cfg || {}));
			next.crowsnest = next.crowsnest || {};
			next.log_level = must('#log_level').value;
			next.show_setup = must('#show_setup').checked;
			next.reseller_mode = must('#reseller_mode').checked;
			next.metric = must('#metric').value;
			next.step_size = num(must('#step_size').value, next.step_size || 0);
			next.margin = Number(num(must('#margin').value, next.margin || 0).toFixed(8));
			next.accepted_mints = collectMints();
			next.profit_share = collectShares();
			next.relays = lines(must('#relays').value);
			next.crowsnest.probe_timeout = num(must('#probe_timeout_s').value, 0) * 1000000000;
			next.crowsnest.probe_retry_count = num(must('#probe_retry_count').value, 0);
			next.crowsnest.probe_retry_delay = num(must('#probe_retry_delay_s').value, 0) * 1000000000;
			next.crowsnest.require_valid_signature = must('#require_valid_signature').checked;
			next.crowsnest.ignore_interfaces = lines(must('#ignore_interfaces').value);
			next.crowsnest.discovery_timeout = num(must('#discovery_timeout_s').value, 0) * 1000000000;
			return next;
		}
		function clientValidate(obj) {
			var errs = [];
			if (!obj.accepted_mints || !obj.accepted_mints.length) errs.push('At least one accepted mint is required.');
			if (!obj.profit_share || !obj.profit_share.length) errs.push('At least one profit share row is required.');
			var total = (obj.profit_share || []).reduce(function(acc, x){ return acc + Number(x.factor || 0); }, 0);
			if (Math.abs(total - 1.0) > 0.001) errs.push('Profit share must total 1.0.');
			if (obj.metric !== 'milliseconds' && obj.metric !== 'bytes') errs.push('Metric must be milliseconds or bytes.');
			return errs;
		}
		function api(action, body) {
			var opts = { method: body == null ? 'GET' : 'POST' };
			if (body != null) {
				opts.headers = { 'Content-Type': 'text/plain;charset=UTF-8' };
				opts.body = body;
			}
			return fetch(API + '?action=' + encodeURIComponent(action), opts).then(function(r){ return r.json(); });
		}
		function applyRead(data, opts) {
			opts = opts || {};
			var walletText = data.wallet || '';
			var walletInfoText = data.wallet_info || '';
			var versionText = data.version || '';
			var statusText = data.status || '';
			var statusKv = keyvals(statusText);
			var versionKv = keyvals(versionText);
			must('#wallet_balance').textContent = walletDisplay(walletText);
			must('#running_state').textContent = boolText(statusKv.running);
			must('#config_ok_state').textContent = boolText(statusKv.config_ok);
			must('#wallet_ok_state').textContent = boolText(statusKv.wallet_ok);
			must('#network_ok_state').textContent = boolText(statusKv.network_ok);
			must('#version_text').textContent = versionKv.version || statusKv.version || 'unknown';
			must('#raw_wallet').textContent = 'wallet balance\n' + walletText;
			must('#raw_wallet_info').textContent = 'wallet info\n' + walletInfoText;
			must('#raw_version').textContent = 'version\n' + versionText;
			must('#raw_status').textContent = 'status\n' + statusText;
			renderLogs(data.logs || '');
			state.cfg = data.config || parseJson(data.config_text || '{}');
			populateForm(state.cfg);
			renderWalletMints(state.cfg, walletInfoText);
			updateLogsStatus('Updated ' + new Date().toLocaleTimeString());
			if (!opts.preserveValidation)
				setValidation(['Loaded config and live status.']);
		}
		function refreshAll(opts) {
			return api('read').then(function(data){ applyRead(data, opts); }).catch(function(err){ setValidation(['Refresh failed: ' + err]); });
		}
		function refreshLogsOnly() {
			if (state.activeTab !== 'logs') return;
			api('logs').then(function(res) {
				renderLogs(res.logs || '');
				updateLogsStatus('Updated ' + new Date().toLocaleTimeString());
			}).catch(function(err) {
				updateLogsStatus('Log refresh failed: ' + err);
			});
		}
		function startLogPolling() {
			if (state.logTimer) return;
			state.logTimer = window.setInterval(function() {
				if (document.hidden || state.activeTab !== 'logs') return;
				refreshLogsOnly();
			}, 5000);
		}
		function setActiveTab(name) {
			state.activeTab = name;
			['overview', 'form', 'json', 'logs'].forEach(function(tab) {
				must('#pane_' + tab).classList.toggle('active', tab === name);
				must('#tab_' + tab).className = tab === name ? 'cbi-button cbi-button-action' : 'cbi-button';
			});
			if (name === 'logs') refreshLogsOnly();
		}
		function validateText(text) {
			return api('validate', text).then(function(res){
				var lines = [res.ok ? 'Validation: OK' : 'Validation: FAILED'];
				if (res.errors && res.errors.length) {
					lines.push('', 'Errors:');
					res.errors.forEach(function(x){ lines.push('- ' + x); });
				}
				if (res.warnings && res.warnings.length) {
					lines.push('', 'Warnings:');
					res.warnings.forEach(function(x){ lines.push('- ' + x); });
				}
				setValidation(lines);
				return res;
			});
		}
		function saveText(text) {
			return api('save', text).then(function(res){
				var lines = [];
				if (res.ok) {
					lines.push('Save: OK');
					lines.push(res.message || 'Saved.');
					if (res.backup) lines.push('Backup: ' + res.backup);
					lines.push('', 'TollGate status after save:', res.status || '');
					setValidation(lines);
					return refreshAll({ preserveValidation: true });
				}
				lines.push('Save: FAILED', res.error || 'Unknown error');
				if (res.errors && res.errors.length) {
					lines.push('', 'Errors:');
					res.errors.forEach(function(x){ lines.push('- ' + x); });
				}
				setValidation(lines);
			});
		}
		function bindHandlers() {
			must('#tab_overview').onclick = function() { setActiveTab('overview'); };
			must('#tab_form').onclick = function() { setActiveTab('form'); };
			must('#tab_json').onclick = function() { setActiveTab('json'); };
			must('#tab_logs').onclick = function() { setActiveTab('logs'); };
			must('#refresh_all').onclick = function() { refreshAll(); };
			must('#add_mint').onclick = function() { var list = collectMints(); list.push({}); renderMints(list); };
			must('#add_share').onclick = function() { var list = collectShares(); list.push({}); renderShares(list); };
			must('#forms_to_json').onclick = function() {
				try {
					var obj = formToObject();
					var errs = clientValidate(obj);
					must('#raw_json').value = pretty(obj);
					setValidation(errs.length ? errs : ['Updated raw JSON from form values.']);
				} catch (e) { setValidation(['Forms → JSON failed: ' + e.message]); }
			};
			must('#json_to_forms').onclick = function() {
				try {
					var obj = parseJson(must('#raw_json').value);
					state.cfg = obj;
					populateForm(obj);
					renderWalletMints(obj, must('#raw_wallet_info').textContent.replace(/^wallet info\n/, ''));
					setValidation(['Loaded form values from raw JSON.']);
				} catch (e) { setValidation(['JSON → Forms failed: ' + e.message]); }
			};
			must('#validate_form').onclick = function() {
				try {
					var obj = formToObject();
					var errs = clientValidate(obj);
					if (errs.length) return setValidation(errs);
					validateText(pretty(obj));
				} catch (e) { setValidation(['Validate form failed: ' + e.message]); }
			};
			must('#save_form').onclick = function() {
				try {
					var obj = formToObject();
					var errs = clientValidate(obj);
					if (errs.length) return setValidation(errs);
					saveText(pretty(obj));
				} catch (e) { setValidation(['Save form failed: ' + e.message]); }
			};
			must('#validate_json').onclick = function() { validateText(must('#raw_json').value); };
			must('#save_json').onclick = function() { saveText(must('#raw_json').value); };
		}

		try {
			bindHandlers();
			startLogPolling();
			refreshAll();
		} catch (e) {
			root.innerHTML = '<div class="cbi-map"><h2>TollGate</h2><pre>UI bootstrap failed: ' + String(e.message || e) + '</pre></div>';
		}

		return root;
	}
});
