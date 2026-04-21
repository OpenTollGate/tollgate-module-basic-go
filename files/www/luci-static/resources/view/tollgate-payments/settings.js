'use strict';
'require view';

return view.extend({
	render: function() {
		var root = E('div', { 'class': 'tollgate-live-page' });

		root.innerHTML = [
			'<style>',
			'.tollgate-live-page{max-width:960px}',
			'.tg-pane{display:none}',
			'.tg-pane.active{display:block}',
			'.tg-tabbar{display:flex;gap:8px;flex-wrap:wrap;margin:0 0 1.2rem 0}',
			'.tg-form-grid{display:grid;grid-template-columns:200px minmax(0,1fr);gap:10px 16px;align-items:center}',
			'.tg-form-grid label{font-weight:600}',
			'.tg-form-grid input,.tg-form-grid select,.tg-form-grid textarea{width:100%}',
			'.tg-actions{display:flex;gap:10px;align-items:center;flex-wrap:wrap;margin-top:12px}',
			'.tg-section-node{margin:0 0 1rem 0;padding:1rem;border:1px solid var(--border-color,#ddd);border-radius:4px}',
			'.tg-logbox{max-height:24rem;overflow:auto;white-space:pre-wrap;font-family:monospace;font-size:13px}',
			'.tg-muted{opacity:.7;font-size:13px}',
			'.tg-status-dot{display:inline-block;width:10px;height:10px;border-radius:50%;margin-right:6px;vertical-align:middle}',
			'.tg-status-dot.ok{background:#5cb85c}',
			'.tg-status-dot.err{background:#d9534f}',
			'.tg-status-dot.warn{background:#f0ad4e}',
			'.tg-status-row{display:flex;gap:2rem;flex-wrap:wrap;padding:.5rem 0}',
			'.tg-status-item{display:flex;align-items:center;gap:4px;font-size:14px}',
			'.tg-balance-big{font-size:28px;font-weight:700;margin:0}',
			'.tg-balance-label{font-size:13px;opacity:.7;margin:0 0 .2rem 0}',
			'.tg-confirm-overlay{position:fixed;top:0;left:0;right:0;bottom:0;background:rgba(0,0,0,.5);display:flex;align-items:center;justify-content:center;z-index:9999}',
			'.tg-confirm-box{background:var(--background-color,#fff);padding:1.5rem;border-radius:8px;max-width:400px;width:90%;box-shadow:0 4px 20px rgba(0,0,0,.3)}',
			'.tg-confirm-box p{margin:0 0 1rem 0;line-height:1.5}',
			'.tg-confirm-box .tg-actions{justify-content:flex-end}',
			'.tg-err{color:#d9534f}',
			'.tg-ok{color:#5cb85c}',
			'.tg-pw-field{display:flex;align-items:center;gap:8px}',
			'.tg-pw-field input{flex:1}',
			'.tg-pw-field button{white-space:nowrap}',
			'</style>',

			'<div class="cbi-map">',
			'<h2 name="content">TollGate</h2>',
			'<div class="cbi-map-descr">Manage your TollGate payment gateway, wallet, and private network.</div>',
			'<div class="tg-tabbar">',
			'<button id="tab_dashboard" class="cbi-button cbi-button-action" type="button">Dashboard</button>',
			'<button id="tab_network" class="cbi-button" type="button">Network</button>',
			'<button id="tab_config" class="cbi-button" type="button">Configuration</button>',
			'<button id="tab_identities" class="cbi-button" type="button">Identities</button>',
			'</div>',

			/* ── Dashboard tab ── */
			'<div id="pane_dashboard" class="tg-pane active">',

			'<div class="cbi-section">',
			'<h3>Service status</h3>',
			'<div class="tg-status-row">',
			'<div class="tg-status-item"><span class="tg-status-dot" id="dot_running"></span><span id="lbl_running">—</span></div>',
			'<div class="tg-status-item"><span class="tg-status-dot" id="dot_wallet"></span><span id="lbl_wallet">—</span></div>',
			'<div class="tg-status-item"><span class="tg-status-dot" id="dot_network"></span><span id="lbl_network">—</span></div>',
			'<div class="tg-status-item tg-muted" id="lbl_version">—</div>',
			'<div class="tg-status-item tg-muted" id="lbl_uptime">—</div>',
			'</div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Wallet</h3>',
			'<div style="display:flex;gap:2rem;flex-wrap:wrap;align-items:flex-start">',
			'<div>',
			'<p class="tg-balance-label">Total balance</p>',
			'<p class="tg-balance-big" id="wallet_balance">—</p>',
			'</div>',
			'<div style="flex:1;min-width:280px">',
			'<label style="font-weight:600;font-size:13px">Fund wallet with Cashu token</label>',
			'<div style="display:flex;gap:8px;margin-top:4px">',
			'<textarea id="fund_token" rows="2" placeholder="Paste Cashu token…" style="flex:1;font-family:monospace;font-size:12px;word-break:break-all;resize:vertical"></textarea>',
			'<button id="btn_fund" class="cbi-button cbi-button-action" type="button" style="align-self:flex-end">Fund</button>',
			'</div>',
			'<div id="fund_status" class="tg-muted" style="margin-top:4px"></div>',
			'</div>',
			'</div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Mint balances</h3>',
			'<table class="table">',
			'<thead><tr><th>Mint</th><th style="width:120px">Balance</th></tr></thead>',
			'<tbody id="mint_rows"><tr><td colspan="2">Loading…</td></tr></tbody>',
			'</table>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Logs</h3>',
			'<div class="cbi-section-descr">Auto-refreshes every 5 seconds. Showing recent <code>tollgate-wrt</code> entries.</div>',
			'<div class="tg-actions"><span id="logs_poll_state" class="tg-muted">Waiting…</span></div>',
			'<pre id="logs_box" class="tg-logbox tg-section-node">Loading…</pre>',
			'</div>',

			'</div>',

			/* ── Network tab ── */
			'<div id="pane_network" class="tg-pane">',

			'<div class="cbi-section">',
			'<h3>Private WiFi network</h3>',
			'<div class="cbi-section-descr">Manage the private WiFi network for authenticated devices.</div>',
			'<div class="tg-status-row" style="margin-bottom:.5rem">',
			'<div class="tg-status-item"><span class="tg-status-dot" id="dot_net"></span><span id="net_enabled_lbl">—</span></div>',
			'<span id="net_status_hint" class="tg-muted"></span>',
			'</div>',
			'<table class="table">',
			'<tr><th style="width:140px">SSID</th><td id="net_ssid">—</td></tr>',
			'<tr><th>Password</th><td><div class="tg-pw-field"><code id="net_pw_display">••••••••</code><button id="btn_toggle_pw" class="cbi-button" type="button" style="padding:2px 10px;font-size:12px">Show</button></div></td></tr>',
			'</table>',
			'<div class="tg-actions">',
			'<button id="btn_net_enable" class="cbi-button cbi-button-action" type="button">Enable</button>',
			'<button id="btn_net_disable" class="cbi-button cbi-button-remove" type="button">Disable</button>',
			'</div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Rename network</h3>',
			'<div class="tg-form-grid tg-section-node">',
			'<label>New SSID</label><input id="net_new_ssid" type="text" placeholder="Enter new network name">',
			'</div>',
			'<div class="tg-actions"><button id="btn_net_rename" class="cbi-button cbi-button-action" type="button">Rename</button><span id="net_rename_status" class="tg-muted"></span></div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Change password</h3>',
			'<div class="tg-form-grid tg-section-node">',
			'<label>New password</label><input id="net_new_password" type="text" placeholder="Leave empty to generate random">',
			'</div>',
			'<div class="tg-actions">',
			'<button id="btn_net_setpw" class="cbi-button cbi-button-action" type="button">Set password</button>',
			'<button id="btn_net_genpw" class="cbi-button cbi-button-add" type="button">Generate random</button>',
			'<span id="net_pw_status" class="tg-muted"></span>',
			'</div>',
			'</div>',

			'</div>',

			/* ── Configuration tab ── */
			'<div id="pane_config" class="tg-pane">',

			'<div class="cbi-section">',
			'<h3>General</h3>',
			'<div class="tg-form-grid tg-section-node">',
			'<label>Config version</label><input id="config_version" type="text">',
			'<label>Log level</label><select id="log_level"><option value="debug">debug</option><option value="info">info</option><option value="warn">warn</option><option value="error">error</option></select>',
			'<label>Step size</label><input id="step_size" type="number">',
			'<label>Margin</label><input id="margin" type="number" step="0.000001">',
			'<label>Metric</label><select id="metric"><option value="milliseconds">milliseconds</option><option value="bytes">bytes</option></select>',
			'<label>Show setup</label><input id="show_setup" type="checkbox">',
			'<label>Reseller mode</label><input id="reseller_mode" type="checkbox">',
			'</div>',
			'</div>',

			'<div class="cbi-section"><h3>Accepted mints</h3><div id="mints"></div><div class="tg-actions"><button id="add_mint" class="cbi-button cbi-button-add" type="button">Add Mint</button></div></div>',

			'<div class="cbi-section"><h3>Profit share</h3><datalist id="identity_datalist"></datalist><div id="shares"></div><div class="tg-actions"><button id="add_share" class="cbi-button cbi-button-add" type="button">Add Share</button><span>Total: <strong id="share_total">0%</strong></span></div></div>',

			'<div class="cbi-section">',
			'<h3>Upstream detector</h3>',
			'<div class="tg-form-grid tg-section-node">',
			'<label>Probe timeout (s)</label><input id="probe_timeout_s" type="number">',
			'<label>Probe retry count</label><input id="probe_retry_count" type="number">',
			'<label>Probe retry delay (s)</label><input id="probe_retry_delay_s" type="number">',
			'<label>Require valid signature</label><input id="require_valid_signature" type="checkbox">',
			'<label>Ignore interfaces</label><textarea id="ignore_interfaces" rows="5"></textarea>',
			'<label>Only interfaces</label><textarea id="only_interfaces" rows="5"></textarea>',
			'<label>Discovery timeout (s)</label><input id="discovery_timeout_s" type="number">',
			'</div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Upstream session manager</h3>',
			'<div class="tg-form-grid tg-section-node">',
			'<label>Max price per millisecond</label><input id="max_price_per_millisecond" type="number" step="0.000001">',
			'<label>Max price per byte</label><input id="max_price_per_byte" type="number" step="0.000001">',
			'<label>Default policy</label><select id="default_policy"><option value="trust_all">trust_all</option><option value="trust_none">trust_none</option></select>',
			'<label>Allowlist</label><textarea id="allowlist" rows="5"></textarea>',
			'<label>Blocklist</label><textarea id="blocklist" rows="5"></textarea>',
			'<label>Preferred session increments milliseconds</label><input id="preferred_session_increments_milliseconds" type="number">',
			'<label>Preferred session increments bytes</label><input id="preferred_session_increments_bytes" type="number">',
			'<label>Millisecond renewal offset</label><input id="millisecond_renewal_offset" type="number">',
			'<label>Bytes renewal offset</label><input id="bytes_renewal_offset" type="number">',
			'<label>Data monitoring interval (s)</label><input id="data_monitoring_interval_s" type="number">',
			'</div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Actions</h3>',
			'<div class="tg-actions">',
			'<button id="validate_form" class="cbi-button cbi-button-action" type="button">Validate</button>',
			'<button id="save_form" class="cbi-button cbi-button-save" type="button">Save configuration</button>',
			'</div>',
			'</div>',

			'<details style="margin-top:1rem">',
			'<summary style="cursor:pointer;font-weight:600;margin-bottom:.5rem">Raw JSON editor</summary>',
			'<div class="tg-section-node"><textarea id="raw_json" style="width:100%;min-height:20rem;font-family:monospace;font-size:12px"></textarea></div>',
			'<div class="tg-actions">',
			'<button id="json_to_forms" class="cbi-button cbi-button-action" type="button">JSON → Forms</button>',
			'<button id="validate_json" class="cbi-button cbi-button-action" type="button">Validate JSON</button>',
			'<button id="save_json" class="cbi-button cbi-button-save" type="button">Save JSON</button>',
			'</div>',
			'</details>',

			'</div>',


			/* ── Identities tab ── */
			'<div id="pane_identities" class="tg-pane">',

			'<div class="cbi-section">',
			'<h3>Owned identities</h3>',
			'<div class="cbi-section-descr">These identities are managed by the system. Private keys are not exposed.</div>',
			'<table class="table">',
			'<thead><tr><th>Name</th></tr></thead>',
			'<tbody id="owned_identities_rows"><tr><td class="tg-muted">Loading…</td></tr></tbody>',
			'</table>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Public identities</h3>',
			'<div class="cbi-section-descr">Identities used for profit sharing and payouts.</div>',
			'<div id="public_identities"></div>',
			'<div class="tg-actions"><button id="btn_add_ident" class="cbi-button cbi-button-add" type="button">Add identity</button></div>',
			'</div>',

			'<div class="cbi-section">',
			'<h3>Actions</h3>',
			'<div class="tg-actions">',
			'<button id="btn_save_identities" class="cbi-button cbi-button-save" type="button">Save identities</button>',
			'<span id="ident_save_status" class="tg-muted"></span>',
			'</div>',
			'</div>',

			'</div>',

			'<div class="cbi-section">',
			'<h3>Messages</h3>',
			'<pre id="validation_box" class="tg-section-node" style="min-height:3rem;font-size:13px"></pre>',
			'</div>',
			'</div>'
		].join('');

		var API = '/cgi-bin/tollgate-api';
		var state = { cfg: {}, identities: {}, logTimer: null, activeTab: 'dashboard', netPwVisible: false, netPw: '', dashTimer: null };

		function q(sel) { return root.querySelector(sel); }
		function qa(sel) { return Array.prototype.slice.call(root.querySelectorAll(sel)); }
		function must(sel) { var el = q(sel); if (!el) throw new Error('Missing: ' + sel); return el; }
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
		function setMsg(lines) { must('#validation_box').textContent = lines.join('\n'); }
		function parseWalletInfo(text) {
			var out = { total: 0, balances: {} };
			String(text || '').split(/\r?\n/).forEach(function(line) {
				var m = line.match(/^\s+(.+):\s+(\d+)\s*$/);
				if (m) out.balances[m[1]] = num(m[2], 0);
				var t = line.match(/total_balance:\s*(\d+)/);
				if (t) out.total = num(t[1], 0);
			});
			return out;
		}
		function renderMintTable(cfg, walletInfoText) {
			var tbody = must('#mint_rows');
			var info = parseWalletInfo(walletInfoText);
			var mints = Array.isArray(cfg.accepted_mints) ? cfg.accepted_mints : [];
			if (!mints.length) {
				tbody.innerHTML = '<tr><td colspan="2" class="tg-muted">No mints configured.</td></tr>';
				return;
			}
			tbody.innerHTML = mints.map(function(mint) {
				var url = mint && mint.url ? mint.url : '';
				var bal = Object.prototype.hasOwnProperty.call(info.balances, url) ? info.balances[url] : 0;
				var cls = bal > 0 ? '' : ' class="tg-muted"';
				return '<tr' + cls + '><td>' + html(url || '(missing URL)') + '</td><td>' + html(String(bal)) + ' sats</td></tr>';
			}).join('');
		}
		function setDot(id, ok) {
			var dot = must(id);
			dot.className = 'tg-status-dot ' + (ok ? 'ok' : 'err');
		}
		function renderLogs(text) {
			var box = must('#logs_box');
			var pinned = (box.scrollTop + box.clientHeight) >= (box.scrollHeight - 24);
			box.textContent = text || 'No tollgate-wrt log entries.';
			if (pinned) box.scrollTop = box.scrollHeight;
		}
		function mintCard(d, idx) {
			d = d || {};
			return '<div class="cbi-section-node mint-card tg-section-node"><h4>Mint #' + (idx + 1) + '</h4><div class="tg-form-grid">' +
			'<label>Url</label><input class="mint-url" value="' + html(d.url || '') + '">' +
			'<label>Min balance</label><input class="mint-min_balance" type="number" value="' + html(d.min_balance || 64) + '">' +
			'<label>Balance tolerance percent</label><input class="mint-balance_tolerance_percent" type="number" value="' + html(d.balance_tolerance_percent || 10) + '">' +
			'<label>Payout interval seconds</label><input class="mint-payout_interval_seconds" type="number" value="' + html(d.payout_interval_seconds || 60) + '">' +
			'<label>Min payout amount</label><input class="mint-min_payout_amount" type="number" value="' + html(d.min_payout_amount || 128) + '">' +
			'<label>Price per step</label><input class="mint-price_per_step" type="number" value="' + html(d.price_per_step || 1) + '">' +
			'<label>Price unit</label><input class="mint-price_unit" value="' + html(d.price_unit || '') + '">' +
			'<label>Purchase min steps</label><input class="mint-purchase_min_steps" type="number" value="' + html(d.purchase_min_steps || 0) + '">' +
			'</div><div class="tg-actions"><button class="cbi-button cbi-button-remove remove-mint" type="button">Remove</button></div></div>';
		}

		function shareCard(s, idx, identitiesData) {
			s = s || {};
			var pct = (typeof s.factor === 'number') ? (s.factor * 100) : 0;
			var identName = s.identity || '';
			var linked = null;
			var linkHint = '';
			var datalistId = 'identity_datalist';

			if (identitiesData && identName) {
				var pubIds = Array.isArray(identitiesData.public_identities) ? identitiesData.public_identities : [];
				for (var i = 0; i < pubIds.length; i++) {
					if (pubIds[i].name === identName) { linked = pubIds[i]; break; }
				}
				if (linked) {
					var pkHint = linked.pubkey ? (linked.pubkey.substring(0, 8) + '…') : 'no pubkey';
					var laHint = linked.lightning_address || 'no lightning';
					linkHint = '<div class="tg-muted" style="font-size:12px;margin-top:2px">↳ ' + html(pkHint) + ' · ' + html(laHint) + '</div>';
				} else {
					linkHint = '<div class="tg-err" style="font-size:12px;margin-top:2px">⚠ Identity not found</div>';
				}
			}

			return '<div class="cbi-section-node share-card tg-section-node"><h4>Share #' + (idx + 1) + '</h4><div class="tg-form-grid">' +
			'<label>Identity</label><div><input class="share-identity" value="' + html(identName) + '" list="' + datalistId + '">' + linkHint + '</div>' +
			'<label>Percent</label><input class="share-percent" type="number" step="0.01" value="' + html(pct) + '">' +
			'</div><div class="tg-actions"><button class="cbi-button cbi-button-remove remove-share" type="button">Remove</button></div></div>';
		}

		function ownedIdentityRow(id) {
			return '<tr><td>' + html(id.name || '') + '</td></tr>';
		}

		function publicIdentityCard(id, idx) {
			var pubkeyDisplay = id.pubkey || '';
			var isPlaceholder = pubkeyDisplay === '[on_setup]';
			var pubkeyHint = isPlaceholder ? ' <span class="tg-muted">(pending setup)</span>' : '';
			return '<div class="cbi-section-node ident-card tg-section-node"><h4>Identity #' + (idx + 1) + '</h4><div class="tg-form-grid">' +
			'<label>Name</label><input class="ident-name" value="' + html(id.name || '') + '">' +
			'<label>PubKey</label><input class="ident-pubkey" value="' + html(pubkeyDisplay) + '" style="font-family:monospace;font-size:12px">' + pubkeyHint +
			'<label>Lightning Address</label><input class="ident-lightning" value="' + html(id.lightning_address || '') + '">' +
			'</div><div class="tg-actions"><button class="cbi-button cbi-button-remove remove-ident" type="button">Remove</button></div></div>';
		}

		function renderMints(list) {
			var wrap = must('#mints');
			wrap.innerHTML = '';
			if (!Array.isArray(list) || !list.length) list = [ {} ];
			list.forEach(function(m, idx){ wrap.insertAdjacentHTML('beforeend', mintCard(m, idx)); });
			qa('.remove-mint').forEach(function(btn){ btn.onclick = function(){ var card = btn.closest('.mint-card'); if (card) card.remove(); }; });
		}

		function renderShares(list, identitiesData) {
			var wrap = must('#shares');
			wrap.innerHTML = '';
			if (!Array.isArray(list) || !list.length) list = [ {} ];
			list.forEach(function(s, idx){ wrap.insertAdjacentHTML('beforeend', shareCard(s, idx, identitiesData)); });
			qa('.remove-share').forEach(function(btn){ btn.onclick = function(){ var card = btn.closest('.share-card'); if (card) card.remove(); updateShareTotal(); }; });
			qa('.share-percent').forEach(function(el){ el.oninput = updateShareTotal; });
			updateShareTotal();
		}

		function updateShareTotal() {
			var total = 0;
			qa('.share-percent').forEach(function(el){ total += num(el.value, 0); });
			must('#share_total').textContent = total.toFixed(2) + '%';
		}

		function renderIdentities(data) {
			var owned = Array.isArray(data.owned_identities) ? data.owned_identities : [];
			var public_ = Array.isArray(data.public_identities) ? data.public_identities : [];

			var ownedWrap = must('#owned_identities_rows');
			ownedWrap.innerHTML = '';
			if (!owned.length) {
				ownedWrap.innerHTML = '<tr><td class="tg-muted">No owned identities.</td></tr>';
			} else {
				owned.forEach(function(id) { ownedWrap.insertAdjacentHTML('beforeend', ownedIdentityRow(id)); });
			}

			var publicWrap = must('#public_identities');
			publicWrap.innerHTML = '';
			if (!public_.length) public_ = [{}];
			public_.forEach(function(id, idx) { publicWrap.insertAdjacentHTML('beforeend', publicIdentityCard(id, idx)); });
			qa('.remove-ident').forEach(function(btn) { btn.onclick = function() { var card = btn.closest('.ident-card'); if (card) card.remove(); }; });
		}

		function updateIdentityDatalist(identitiesData) {
			var dl = must('#identity_datalist');
			var names = [];
			if (identitiesData && Array.isArray(identitiesData.public_identities)) {
				identitiesData.public_identities.forEach(function(id) {
					if (id.name) names.push(id.name);
				});
			}
			dl.innerHTML = names.map(function(n) { return '<option value="' + html(n) + '">'; }).join('');
		}

		function collectMints() {
			return qa('.mint-card').map(function(card){
				return {
					url: card.querySelector('.mint-url').value.trim(),
					min_balance: num(card.querySelector('.mint-min_balance').value, 64),
					balance_tolerance_percent: num(card.querySelector('.mint-balance_tolerance_percent').value, 10),
					payout_interval_seconds: num(card.querySelector('.mint-payout_interval_seconds').value, 60),
					min_payout_amount: num(card.querySelector('.mint-min_payout_amount').value, 128),
					price_per_step: num(card.querySelector('.mint-price_per_step').value, 1),
					price_unit: card.querySelector('.mint-price_unit').value.trim(),
					purchase_min_steps: num(card.querySelector('.mint-purchase_min_steps').value, 0),
				};
			}).filter(function(m){ return m.url.length > 0; });
		}

		function collectShares() {
			return qa('.share-card').map(function(card){
				var pct = num(card.querySelector('.share-percent').value, 0);
				return { identity: card.querySelector('.share-identity').value.trim(), factor: Number((pct / 100).toFixed(8)) };
			}).filter(function(s){ return s.identity.length > 0; });
		}

		function collectPublicIdentities() {
			return qa('.ident-card').map(function(card) {
				return {
					name: card.querySelector('.ident-name').value.trim(),
					pubkey: card.querySelector('.ident-pubkey').value.trim(),
					lightning_address: card.querySelector('.ident-lightning').value.trim()
				};
			}).filter(function(id) { return id.name.length > 0; });
		}

		function populateForm(cfg) {
			cfg = cfg || {};
			cfg.accepted_mints = Array.isArray(cfg.accepted_mints) ? cfg.accepted_mints : [];
			cfg.profit_share = Array.isArray(cfg.profit_share) ? cfg.profit_share : [];
			cfg.upstream_detector = cfg.upstream_detector || {};
			cfg.upstream_session_manager = cfg.upstream_session_manager || {};
			cfg.upstream_session_manager.trust = cfg.upstream_session_manager.trust || {};
			cfg.upstream_session_manager.sessions = cfg.upstream_session_manager.sessions || {};
			cfg.upstream_session_manager.usage_tracking = cfg.upstream_session_manager.usage_tracking || {};
			must('#config_version').value = cfg.config_version || '';
			must('#log_level').value = cfg.log_level || 'debug';
			must('#step_size').value = cfg.step_size || 0;
			must('#margin').value = cfg.margin || 0;
			must('#metric').value = cfg.metric || 'milliseconds';
			must('#show_setup').checked = !!cfg.show_setup;
			must('#reseller_mode').checked = !!cfg.reseller_mode;
			must('#probe_timeout_s').value = Math.round(num(cfg.upstream_detector.probe_timeout, 0) / 1000000000);
			must('#probe_retry_count').value = cfg.upstream_detector.probe_retry_count || 0;
			must('#probe_retry_delay_s').value = Math.round(num(cfg.upstream_detector.probe_retry_delay, 0) / 1000000000);
			must('#require_valid_signature').checked = !!cfg.upstream_detector.require_valid_signature;
			must('#ignore_interfaces').value = (cfg.upstream_detector.ignore_interfaces || []).join('\n');
			must('#only_interfaces').value = (cfg.upstream_detector.only_interfaces || []).join('\n');
			must('#discovery_timeout_s').value = Math.round(num(cfg.upstream_detector.discovery_timeout, 0) / 1000000000);
			must('#max_price_per_millisecond').value = cfg.upstream_session_manager.max_price_per_millisecond || 0;
			must('#max_price_per_byte').value = cfg.upstream_session_manager.max_price_per_byte || 0;
			must('#default_policy').value = cfg.upstream_session_manager.trust.default_policy || 'trust_all';
			must('#allowlist').value = (cfg.upstream_session_manager.trust.allowlist || []).join('\n');
			must('#blocklist').value = (cfg.upstream_session_manager.trust.blocklist || []).join('\n');
			must('#preferred_session_increments_milliseconds').value = cfg.upstream_session_manager.sessions.preferred_session_increments_milliseconds || 0;
			must('#preferred_session_increments_bytes').value = cfg.upstream_session_manager.sessions.preferred_session_increments_bytes || 0;
			must('#millisecond_renewal_offset').value = cfg.upstream_session_manager.sessions.millisecond_renewal_offset || 0;
			must('#bytes_renewal_offset').value = cfg.upstream_session_manager.sessions.bytes_renewal_offset || 0;
			must('#data_monitoring_interval_s').value = Math.round(num(cfg.upstream_session_manager.usage_tracking.data_monitoring_interval, 0) / 1000000000);
			renderMints(cfg.accepted_mints);
			renderShares(cfg.profit_share, state.identities);
			must('#raw_json').value = pretty(cfg);
		}

		function formToObject() {
			var next = JSON.parse(JSON.stringify(state.cfg || {}));
			next.upstream_detector = next.upstream_detector || {};
			next.upstream_session_manager = next.upstream_session_manager || {};
			next.upstream_session_manager.trust = next.upstream_session_manager.trust || {};
			next.upstream_session_manager.sessions = next.upstream_session_manager.sessions || {};
			next.upstream_session_manager.usage_tracking = next.upstream_session_manager.usage_tracking || {};
			next.config_version = must('#config_version').value;
			next.log_level = must('#log_level').value;
			next.step_size = num(must('#step_size').value, next.step_size || 0);
			next.margin = Number(num(must('#margin').value, next.margin || 0).toFixed(8));
			next.metric = must('#metric').value;
			next.show_setup = must('#show_setup').checked;
			next.reseller_mode = must('#reseller_mode').checked;
			next.upstream_detector.probe_timeout = num(must('#probe_timeout_s').value, 0) * 1000000000;
			next.upstream_detector.probe_retry_count = num(must('#probe_retry_count').value, next.upstream_detector.probe_retry_count || 0);
			next.upstream_detector.probe_retry_delay = num(must('#probe_retry_delay_s').value, 0) * 1000000000;
			next.upstream_detector.require_valid_signature = must('#require_valid_signature').checked;
			next.upstream_detector.ignore_interfaces = lines(must('#ignore_interfaces').value);
			next.upstream_detector.only_interfaces = lines(must('#only_interfaces').value);
			next.upstream_detector.discovery_timeout = num(must('#discovery_timeout_s').value, 0) * 1000000000;
			next.upstream_session_manager.max_price_per_millisecond = Number(num(must('#max_price_per_millisecond').value, next.upstream_session_manager.max_price_per_millisecond || 0).toFixed(8));
			next.upstream_session_manager.max_price_per_byte = Number(num(must('#max_price_per_byte').value, next.upstream_session_manager.max_price_per_byte || 0).toFixed(8));
			next.upstream_session_manager.trust.default_policy = must('#default_policy').value;
			next.upstream_session_manager.trust.allowlist = lines(must('#allowlist').value);
			next.upstream_session_manager.trust.blocklist = lines(must('#blocklist').value);
			next.upstream_session_manager.sessions.preferred_session_increments_milliseconds = num(must('#preferred_session_increments_milliseconds').value, next.upstream_session_manager.sessions.preferred_session_increments_milliseconds || 0);
			next.upstream_session_manager.sessions.preferred_session_increments_bytes = num(must('#preferred_session_increments_bytes').value, next.upstream_session_manager.sessions.preferred_session_increments_bytes || 0);
			next.upstream_session_manager.sessions.millisecond_renewal_offset = num(must('#millisecond_renewal_offset').value, next.upstream_session_manager.sessions.millisecond_renewal_offset || 0);
			next.upstream_session_manager.sessions.bytes_renewal_offset = num(must('#bytes_renewal_offset').value, next.upstream_session_manager.sessions.bytes_renewal_offset || 0);
			next.upstream_session_manager.usage_tracking.data_monitoring_interval = num(must('#data_monitoring_interval_s').value, 0) * 1000000000;
			next.accepted_mints = collectMints();
			next.profit_share = collectShares();
			return next;
		}

		function clientValidate(obj) {
			var errs = [];
			if (!obj.accepted_mints || !obj.accepted_mints.length) errs.push('At least one accepted mints is required.');
			if (!obj.profit_share || !obj.profit_share.length) errs.push('At least one profit share is required.');
			var total = (obj.profit_share || []).reduce(function(acc, x){ return acc + Number(x.factor || 0); }, 0);
			if (Math.abs(total - 1.0) > 0.001) errs.push('Profit share must total 100%.');
			if (obj.log_level !== 'debug' && obj.log_level !== 'info' && obj.log_level !== 'warn' && obj.log_level !== 'error') errs.push('Log level must be one of: debug, info, warn, error.');
			if (obj.metric !== 'milliseconds' && obj.metric !== 'bytes') errs.push('Metric must be one of: milliseconds, bytes.');
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
		function cleanCliError(text) {
			return (text || '').split('\n').filter(function(l) {
				return l.indexOf('Error:') === 0 || l.indexOf('Failed') >= 0 || l.indexOf('invalid') >= 0;
			}).join(' ').replace(/Error:\s*/g, '').replace(/command failed/g, '').trim() || text;
		}
		function confirm(message, onConfirm) {
			var overlay = document.createElement('div');
			overlay.className = 'tg-confirm-overlay';
			overlay.innerHTML = '<div class="tg-confirm-box"><p>' + html(message) + '</p><div class="tg-actions">' +
				'<button class="cbi-button" type="button" id="tg_confirm_no">Cancel</button>' +
				'<button class="cbi-button cbi-button-remove" type="button" id="tg_confirm_yes">Confirm</button>' +
				'</div></div>';
			document.body.appendChild(overlay);
			overlay.querySelector('#tg_confirm_no').onclick = function() { document.body.removeChild(overlay); };
			overlay.querySelector('#tg_confirm_yes').onclick = function() { document.body.removeChild(overlay); onConfirm(); };
			overlay.onclick = function(e) { if (e.target === overlay) document.body.removeChild(overlay); };
		}
		function applyDashboard(data) {
			var walletText = data.wallet || '';
			var walletInfoText = data.wallet_info || '';
			var statusText = data.status || '';
			var statusKv = keyvals(statusText);
			var versionText = data.version || '';
			var versionKv = keyvals(versionText);

			var running = statusKv.running === 'true';
			var walletOk = statusKv.wallet_ok === 'true';
			var networkOk = statusKv.network_ok === 'true';

			setDot('#dot_running', running);
			setDot('#dot_wallet', walletOk);
			setDot('#dot_network', networkOk);
			must('#lbl_running').textContent = running ? 'Running' : 'Stopped';
			must('#lbl_wallet').textContent = walletOk ? 'Wallet OK' : 'Wallet issue';
			must('#lbl_network').textContent = networkOk ? 'Network OK' : 'Network issue';
			must('#lbl_version').textContent = versionKv.version || '';
			var uptime = statusKv.uptime || '';
			must('#lbl_uptime').textContent = uptime ? 'Uptime: ' + uptime.replace(/h.*$/, '') : '';

			var m = String(walletText).match(/(\d+)\s*sats/i);
			must('#wallet_balance').textContent = m ? m[1] + ' sats' : '—';

			state.cfg = data.config || {};
			state.identities = data.identities || {};
			renderIdentities(state.identities);
			updateIdentityDatalist(state.identities);
			populateForm(state.cfg);
			renderMintTable(state.cfg, walletInfoText);
			renderLogs(data.logs || '');
			must('#logs_poll_state').textContent = 'Updated ' + new Date().toLocaleTimeString();
		}
		function refreshDashboard() {
			return api('read').then(function(data){ applyDashboard(data); }).catch(function(err){ setMsg(['Refresh failed: ' + err]); });
		}
		function refreshLogsOnly() {
			if (state.activeTab !== 'dashboard') return;
			api('logs').then(function(res) {
				renderLogs(res.logs || '');
				must('#logs_poll_state').textContent = 'Updated ' + new Date().toLocaleTimeString();
			});
		}
		function doWalletFund() {
			var token = must('#fund_token').value.trim();
			if (!token) { must('#fund_status').textContent = 'Paste a Cashu token first.'; return; }
			must('#fund_status').textContent = 'Funding…';
			must('#btn_fund').disabled = true;
			api('wallet_fund', JSON.stringify({ token: token })).then(function(res) {
				must('#btn_fund').disabled = false;
				if (res.ok) {
					must('#fund_status').innerHTML = '<span class="tg-ok">Funded ' + (res.amount_received || 0) + ' sats</span>';
					must('#fund_token').value = '';
					refreshDashboard();
				} else {
					must('#fund_status').innerHTML = '<span class="tg-err">' + html(cleanCliError(res.error || 'Unknown error')) + '</span>';
				}
			}).catch(function(err) {
				must('#btn_fund').disabled = false;
				must('#fund_status').innerHTML = '<span class="tg-err">Request failed: ' + html(String(err)) + '</span>';
			});
		}
		function renderNetworkStatus(data) {
			must('#net_ssid').textContent = data.ssid || '—';
			state.netPw = data.password || '';
			updateNetPwDisplay();
			var enabled = !!data.enabled;
			setDot('#dot_net', enabled);
			must('#net_enabled_lbl').textContent = enabled ? 'Enabled' : 'Disabled';
			must('#net_status_hint').textContent = 'Updated ' + new Date().toLocaleTimeString();
		}
		function updateNetPwDisplay() {
			var display = must('#net_pw_display');
			var btn = must('#btn_toggle_pw');
			if (state.netPwVisible) {
				display.textContent = state.netPw || '(not set)';
				btn.textContent = 'Hide';
			} else {
				display.textContent = state.netPw ? '••••••••••' : '(not set)';
				btn.textContent = 'Show';
			}
		}
		function refreshNetworkStatus() {
			must('#net_status_hint').textContent = 'Loading…';
			api('network_status').then(function(res) {
				if (res.ok) renderNetworkStatus(res);
				else must('#net_status_hint').innerHTML = '<span class="tg-err">' + html(res.error || 'Error') + '</span>';
			});
		}
		function doNetworkEnable() {
			must('#net_status_hint').textContent = 'Enabling…';
			api('network_enable').then(function(res) {
				must('#net_status_hint').innerHTML = res.ok
					? '<span class="tg-ok">Enabled</span>'
					: '<span class="tg-err">' + html(res.error || 'Error') + '</span>';
				refreshNetworkStatus();
			});
		}
		function doNetworkDisable() {
			confirm('Disable the private WiFi network? This may disconnect devices currently using it.', function() {
				must('#net_status_hint').textContent = 'Disabling…';
				api('network_disable').then(function(res) {
					must('#net_status_hint').innerHTML = res.ok
						? '<span class="tg-ok">Disabled</span>'
						: '<span class="tg-err">' + html(res.error || 'Error') + '</span>';
					refreshNetworkStatus();
				});
			});
		}
		function doNetworkRename() {
			var newSsid = must('#net_new_ssid').value.trim();
			if (!newSsid) { must('#net_rename_status').textContent = 'Enter a new SSID.'; return; }
			must('#net_rename_status').textContent = 'Renaming…';
			api('network_rename', JSON.stringify({ ssid: newSsid })).then(function(res) {
				must('#net_rename_status').innerHTML = res.ok
					? '<span class="tg-ok">Renamed to ' + html(newSsid) + '</span>'
					: '<span class="tg-err">' + html(res.error || 'Error') + '</span>';
				if (res.ok) must('#net_new_ssid').value = '';
				refreshNetworkStatus();
			});
		}
		function doNetworkSetPassword(generate) {
			var pw = generate ? '' : must('#net_new_password').value.trim();
			if (!generate && pw.length > 0 && (pw.length < 8 || pw.length > 63)) {
				must('#net_pw_status').innerHTML = '<span class="tg-err">Password must be 8–63 characters.</span>';
				return;
			}
			must('#net_pw_status').textContent = generate ? 'Generating…' : 'Setting…';
			api('network_set_password', JSON.stringify({ password: pw })).then(function(res) {
				if (res.ok) {
					var msg = res.new_password ? 'New password: ' + res.new_password : 'Password changed';
					must('#net_pw_status').innerHTML = '<span class="tg-ok">' + html(msg) + '</span>';
					if (generate && res.new_password) must('#net_new_password').value = res.new_password;
					refreshNetworkStatus();
				} else {
					must('#net_pw_status').innerHTML = '<span class="tg-err">' + html(res.error || 'Error') + '</span>';
				}
			});
		}
		function validateText(text) {
			return api('validate', text).then(function(res){
				var lines = [res.ok ? 'Validation: OK' : 'Validation: FAILED'];
				if (res.errors && res.errors.length) { lines.push('', 'Errors:'); res.errors.forEach(function(x){ lines.push('- ' + x); }); }
				if (res.warnings && res.warnings.length) { lines.push('', 'Warnings:'); res.warnings.forEach(function(x){ lines.push('- ' + x); }); }
				setMsg(lines);
				return res;
			});
		}
		function saveText(text) {
			return api('save', text).then(function(res){
				if (res.ok) {
					var lines = ['Saved.', res.message || ''];
					if (res.backup) lines.push('Backup: ' + res.backup);
					setMsg(lines);
					return refreshDashboard();
				}
				setMsg(['Save FAILED', res.error || 'Unknown error']);
			});
		}
		function doSaveIdentities() {
			var identitiesObj = JSON.parse(JSON.stringify(state.identities || {}));
			identitiesObj.public_identities = collectPublicIdentities();
			if (!identitiesObj.config_version) identitiesObj.config_version = 'v0.0.1';
			if (!identitiesObj.owned_identities) identitiesObj.owned_identities = [];
			must('#ident_save_status').textContent = 'Saving…';
			api('save_identities', JSON.stringify(identitiesObj)).then(function(res) {
				if (res.ok) {
					must('#ident_save_status').innerHTML = '<span class="tg-ok">Saved</span>';
					refreshDashboard();
				} else {
					must('#ident_save_status').innerHTML = '<span class="tg-err">' + html(res.error || 'Error') + '</span>';
				}
			});
		}
		function setActiveTab(name) {
			state.activeTab = name;
			['dashboard', 'network', 'config', 'identities'].forEach(function(tab) {
				must('#pane_' + tab).classList.toggle('active', tab === name);
				must('#tab_' + tab).className = tab === name ? 'cbi-button cbi-button-action' : 'cbi-button';
			});
			if (name === 'network') refreshNetworkStatus();
		}
		function startPolling() {
			if (state.logTimer) return;
			state.logTimer = window.setInterval(function() {
				if (document.hidden) return;
				if (state.activeTab === 'dashboard') refreshLogsOnly();
			}, 5000);
			state.dashTimer = window.setInterval(function() {
				if (document.hidden) return;
				if (state.activeTab === 'dashboard') refreshDashboard();
			}, 30000);
		}
		function bindHandlers() {
			must('#tab_dashboard').onclick = function() { setActiveTab('dashboard'); };
			must('#tab_network').onclick = function() { setActiveTab('network'); };
			must('#tab_config').onclick = function() { setActiveTab('config'); };
			must('#tab_identities').onclick = function() { setActiveTab('identities'); };

			must('#btn_fund').onclick = function() { doWalletFund(); };

			must('#btn_toggle_pw').onclick = function() { state.netPwVisible = !state.netPwVisible; updateNetPwDisplay(); };
			must('#btn_net_enable').onclick = function() { doNetworkEnable(); };
			must('#btn_net_disable').onclick = function() { doNetworkDisable(); };
			must('#btn_net_rename').onclick = function() { doNetworkRename(); };
			must('#btn_net_setpw').onclick = function() { doNetworkSetPassword(false); };
			must('#btn_net_genpw').onclick = function() { doNetworkSetPassword(true); };

			must('#add_mint').onclick = function() { var list = collectMints(); list.push({}); renderMints(list); };
			must('#add_share').onclick = function() { var list = collectShares(); list.push({}); renderShares(list, state.identities); };
			must('#btn_add_ident').onclick = function() {
				var data = state.identities || {};
				var list = Array.isArray(data.public_identities) ? collectPublicIdentities() : [];
				list.push({});
				data.public_identities = list;
				renderIdentities(data);
			};
			must('#btn_save_identities').onclick = function() { doSaveIdentities(); };

			must('#validate_form').onclick = function() {
				try {
					var obj = formToObject();
					var errs = clientValidate(obj);
					if (errs.length) return setMsg(errs);
					validateText(pretty(obj));
				} catch (e) { setMsg(['Validate failed: ' + e.message]); }
			};
			must('#save_form').onclick = function() {
				try {
					var obj = formToObject();
					var errs = clientValidate(obj);
					if (errs.length) return setMsg(errs);
					saveText(pretty(obj));
				} catch (e) { setMsg(['Save failed: ' + e.message]); }
			};
			must('#json_to_forms').onclick = function() {
				try {
					var obj = parseJson(must('#raw_json').value);
					state.cfg = obj;
					populateForm(obj);
					setMsg(['Loaded form values from JSON.']);
				} catch (e) { setMsg(['JSON → Forms failed: ' + e.message]); }
			};
			must('#validate_json').onclick = function() { validateText(must('#raw_json').value); };
			must('#save_json').onclick = function() { saveText(must('#raw_json').value); };
		}

		try {
			bindHandlers();
			startPolling();
			refreshDashboard();
		} catch (e) {
			root.innerHTML = '<div class="cbi-map"><h2>TollGate</h2><pre>UI error: ' + html(String(e.message || e)) + '</pre></div>';
		}

		return root;
	}
});
