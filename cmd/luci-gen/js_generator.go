package main

import (
	"fmt"
	"strings"
)

func generateJS(schema *UISchema, structs map[string]*StructDef) string {
	var b strings.Builder

	b.WriteString("'use strict';\n'require view';\n\nreturn view.extend({\n\trender: function() {\n\t\tvar root = E('div', { 'class': 'tollgate-live-page' });\n\n")
	b.WriteString("\t\troot.innerHTML = [\n")
	jsCSS(&b)
	jsTabBar(&b)
	jsDashboardTab(&b)
	jsNetworkTab(&b)
	jsConfigTab(&b, schema)
	jsIdentitiesTab(&b)
	jsMessages(&b)
	b.WriteString("\t\t].join('');\n\n")
	jsState(&b)
	jsHelpers(&b)
	jsCardFunctions(&b, schema, structs)
	jsRenderFunctions(&b, schema, structs)
	jsCollectFunctions(&b, schema, structs)
	jsFormFunctions(&b, schema, structs)
	jsAPIFunctions(&b)
	jsConfirmFunction(&b)
	jsDashboardFunctions(&b)
	jsWalletFunctions(&b)
	jsNetworkFunctions(&b)
	jsConfigSaveFunctions(&b)
	jsIdentitiesSaveFunction(&b)
	jsTabSwitching(&b)
	jsPolling(&b)
	jsBindHandlers(&b, schema, structs)
	jsInit(&b)
	b.WriteString("\t}\n});\n")

	return b.String()
}

// ── Static HTML sections ──────────────────────────────────

func jsCSS(b *strings.Builder) {
	b.WriteString(strings.ReplaceAll(cssBlock, "\n", "\n"))
}

const cssBlock = `			'<style>',
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
`

func jsTabBar(b *strings.Builder) {
	b.WriteString(tabBarBlock)
}

const tabBarBlock = `
			'<div class="cbi-map">',
			'<h2 name="content">TollGate</h2>',
			'<div class="cbi-map-descr">Manage your TollGate payment gateway, wallet, and private network.</div>',
			'<div class="tg-tabbar">',
			'<button id="tab_dashboard" class="cbi-button cbi-button-action" type="button">Dashboard</button>',
			'<button id="tab_network" class="cbi-button" type="button">Network</button>',
			'<button id="tab_config" class="cbi-button" type="button">Configuration</button>',
			'<button id="tab_identities" class="cbi-button" type="button">Identities</button>',
			'</div>',
`

func jsDashboardTab(b *strings.Builder) {
	b.WriteString(dashboardBlock)
}

const dashboardBlock = `
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
`

func jsNetworkTab(b *strings.Builder) {
	b.WriteString(networkBlock)
}

const networkBlock = `
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
`

func jsMessages(b *strings.Builder) {
	b.WriteString(messagesBlock)
}

const messagesBlock = `
			'<div class="cbi-section">',
			'<h3>Messages</h3>',
			'<pre id="validation_box" class="tg-section-node" style="min-height:3rem;font-size:13px"></pre>',
			'</div>',
			'</div>'
`

func jsState(b *strings.Builder) {
	b.WriteString("\t\tvar API = '/cgi-bin/tollgate-api';\n")
	b.WriteString("\t\tvar state = { cfg: {}, identities: {}, logTimer: null, activeTab: 'dashboard', netPwVisible: false, netPw: '', dashTimer: null };\n\n")
}

// ── Helpers ───────────────────────────────────────────────

func jsHelpers(b *strings.Builder) {
	b.WriteString(helperFuncs)
}

const helperFuncs = `		function q(sel) { return root.querySelector(sel); }
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
`

// ── Generated: Configuration tab HTML ─────────────────────

func jsConfigTab(b *strings.Builder, schema *UISchema) {
	b.WriteString("\n\t\t\t/* ── Configuration tab ── */\n")
	b.WriteString("\t\t\t'<div id=\"pane_config\" class=\"tg-pane\">',\n\n")

	// General section
	b.WriteString("\t\t\t'<div class=\"cbi-section\">',\n")
	b.WriteString("\t\t\t'<h3>General</h3>',\n")
	b.WriteString("\t\t\t'<div class=\"tg-form-grid tg-section-node\">',\n")
	for _, f := range schema.TopLevelFields {
		writeFieldHTML(b, f)
	}
	b.WriteString("\t\t\t'</div>',\n")
	b.WriteString("\t\t\t'</div>',\n\n")

	// Card and flat sections
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if sec.IsArray {
			writeCardSectionHTML(b, sec)
		} else {
			writeFlatSectionHTML(b, sec)
		}
	}

	// Actions
	b.WriteString("\t\t\t'<div class=\"cbi-section\">',\n")
	b.WriteString("\t\t\t'<h3>Actions</h3>',\n")
	b.WriteString("\t\t\t'<div class=\"tg-actions\">',\n")
	b.WriteString("\t\t\t'<button id=\"validate_form\" class=\"cbi-button cbi-button-action\" type=\"button\">Validate</button>',\n")
	b.WriteString("\t\t\t'<button id=\"save_form\" class=\"cbi-button cbi-button-save\" type=\"button\">Save configuration</button>',\n")
	b.WriteString("\t\t\t'</div>',\n")
	b.WriteString("\t\t\t'</div>',\n\n")

	// Raw JSON editor
	b.WriteString("\t\t\t'<details style=\"margin-top:1rem\">',\n")
	b.WriteString("\t\t\t'<summary style=\"cursor:pointer;font-weight:600;margin-bottom:.5rem\">Raw JSON editor</summary>',\n")
	b.WriteString("\t\t\t'<div class=\"tg-section-node\"><textarea id=\"raw_json\" style=\"width:100%;min-height:20rem;font-family:monospace;font-size:12px\"></textarea></div>',\n")
	b.WriteString("\t\t\t'<div class=\"tg-actions\">',\n")
	b.WriteString("\t\t\t'<button id=\"json_to_forms\" class=\"cbi-button cbi-button-action\" type=\"button\">JSON → Forms</button>',\n")
	b.WriteString("\t\t\t'<button id=\"validate_json\" class=\"cbi-button cbi-button-action\" type=\"button\">Validate JSON</button>',\n")
	b.WriteString("\t\t\t'<button id=\"save_json\" class=\"cbi-button cbi-button-save\" type=\"button\">Save JSON</button>',\n")
	b.WriteString("\t\t\t'</div>',\n")
	b.WriteString("\t\t\t'</details>',\n\n")

	b.WriteString("\t\t\t'</div>',\n\n")
}

func writeFieldHTML(b *strings.Builder, f UIField) {
	switch f.InputType {
	case InputSelect:
		fmt.Fprintf(b, "\t\t\t'<label>%s</label><select id=\"%s\">", f.Label, f.HTMLID)
		for _, opt := range f.SelectOpts {
			fmt.Fprintf(b, "<option value=\"%s\">%s</option>", opt, opt)
		}
		b.WriteString("</select>',\n")
	case InputCheckbox:
		fmt.Fprintf(b, "\t\t\t'<label>%s</label><input id=\"%s\" type=\"checkbox\">',\n", f.Label, f.HTMLID)
	case InputNumber, InputNumberDuration:
		fmt.Fprintf(b, "\t\t\t'<label>%s</label><input id=\"%s\" type=\"number\">',\n", f.Label, f.HTMLID)
	case InputNumberFloat:
		fmt.Fprintf(b, "\t\t\t'<label>%s</label><input id=\"%s\" type=\"number\" step=\"0.000001\">',\n", f.Label, f.HTMLID)
	case InputTextareaLines:
		fmt.Fprintf(b, "\t\t\t'<label>%s</label><textarea id=\"%s\" rows=\"5\"></textarea>',\n", f.Label, f.HTMLID)
	default:
		fmt.Fprintf(b, "\t\t\t'<label>%s</label><input id=\"%s\" type=\"text\">',\n", f.Label, f.HTMLID)
	}
}

func writeCardSectionHTML(b *strings.Builder, sec *UISection) {
	prefix := deriveCardPrefix(sec.JSONKey)
	singular := capitalizeUpper(prefix)

	if sec.StructName == "ProfitShareConfig" {
		fmt.Fprintf(b, "\t\t\t'<div class=\"cbi-section\"><h3>%s</h3><datalist id=\"identity_datalist\"></datalist><div id=\"%ss\"></div><div class=\"tg-actions\"><button id=\"add_%s\" class=\"cbi-button cbi-button-add\" type=\"button\">Add %s</button><span>Total: <strong id=\"share_total\">0%%</strong></span></div></div>',\n\n",
			sec.Name, prefix, prefix, singular)
	} else {
		fmt.Fprintf(b, "\t\t\t'<div class=\"cbi-section\"><h3>%s</h3><div id=\"%ss\"></div><div class=\"tg-actions\"><button id=\"add_%s\" class=\"cbi-button cbi-button-add\" type=\"button\">Add %s</button></div></div>',\n\n",
			sec.Name, prefix, prefix, singular)
	}
}

func writeFlatSectionHTML(b *strings.Builder, sec *UISection) {
	b.WriteString("\t\t\t'<div class=\"cbi-section\">',\n")
	fmt.Fprintf(b, "\t\t\t'<h3>%s</h3>',\n", sec.Name)
	b.WriteString("\t\t\t'<div class=\"tg-form-grid tg-section-node\">',\n")
	for _, f := range sec.Fields {
		writeFieldHTML(b, f)
	}
	b.WriteString("\t\t\t'</div>',\n")
	b.WriteString("\t\t\t'</div>',\n\n")
}

// ── Generated: Identities tab HTML ────────────────────────

func jsIdentitiesTab(b *strings.Builder) {
	b.WriteString(identitiesTabBlock)
}

const identitiesTabBlock = `
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
`

// ── Generated: Card functions ─────────────────────────────

func jsCardFunctions(b *strings.Builder, schema *UISchema, structs map[string]*StructDef) {
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if !sec.IsArray {
			continue
		}
		prefix := deriveCardPrefix(sec.JSONKey)
		singular := capitalizeUpper(prefix)

		if sec.StructName == "ProfitShareConfig" {
			writeShareCardFunction(b)
		} else {
			writeGenericCardFunction(b, sec, prefix, singular)
		}
	}

	// Identity card functions
	writeOwnedIdentityRow(b)
	writePublicIdentityCard(b, structs)
}

func writeGenericCardFunction(b *strings.Builder, sec *UISection, prefix, singular string) {
	fmt.Fprintf(b, "\t\tfunction %sCard(d, idx) {\n", prefix)
	b.WriteString("\t\t\td = d || {};\n")
	fmt.Fprintf(b, "\t\t\treturn '<div class=\"cbi-section-node %s-card tg-section-node\"><h4>%s #' + (idx + 1) + '</h4><div class=\"tg-form-grid\">' +\n", prefix, singular)

	for _, f := range sec.Fields {
		switch f.InputType {
		case InputText:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><input class=\"%s\" value=\"' + html(d.%s || '') + '\">' +\n", f.Label, f.CSSClass, f.JSONKey)
		case InputNumber:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><input class=\"%s\" type=\"number\" value=\"' + html(d.%s || %s) + '\">' +\n", f.Label, f.CSSClass, f.JSONKey, f.Default)
		case InputNumberFloat:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><input class=\"%s\" type=\"number\" step=\"0.000001\" value=\"' + html(d.%s || %s) + '\">' +\n", f.Label, f.CSSClass, f.JSONKey, f.Default)
		case InputCheckbox:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><input class=\"%s\" type=\"checkbox\"' + (d.%s ? ' checked' : '') + '>' +\n", f.Label, f.CSSClass, f.JSONKey)
		case InputTextareaLines:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><textarea class=\"%s\" rows=\"4\">' + html((d.%s || []).join(\"\\n\")) + '</textarea>' +\n", f.Label, f.CSSClass, f.JSONKey)
		case InputSelect:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><select class=\"%s\">'", f.Label, f.CSSClass)
			for _, opt := range f.SelectOpts {
				fmt.Fprintf(b, "+ '<option value=\"%s\">%s</option>'", opt, opt)
			}
			b.WriteString(" + '</select>' +\n")
		case InputNumberDuration:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><input class=\"%s\" type=\"number\" value=\"' + html(Math.round(num(d.%s, 0) / 1000000000)) + '\">' +\n", f.Label, f.CSSClass, f.JSONKey)
		default:
			fmt.Fprintf(b, "\t\t\t'<label>%s</label><input class=\"%s\" value=\"' + html(d.%s || '') + '\">' +\n", f.Label, f.CSSClass, f.JSONKey)
		}
	}

	fmt.Fprintf(b, "\t\t\t'</div><div class=\"tg-actions\"><button class=\"cbi-button cbi-button-remove remove-%s\" type=\"button\">Remove</button></div></div>';\n", prefix)
	b.WriteString("\t\t}\n\n")
}

func writeShareCardFunction(b *strings.Builder) {
	b.WriteString(shareCardFunc)
}

const shareCardFunc = `		function shareCard(s, idx, identitiesData) {
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

`

func writeOwnedIdentityRow(b *strings.Builder) {
	b.WriteString(ownedIdentityRowFunc)
}

const ownedIdentityRowFunc = `		function ownedIdentityRow(id) {
			return '<tr><td>' + html(id.name || '') + '</td></tr>';
		}

`

func writePublicIdentityCard(b *strings.Builder, structs map[string]*StructDef) {
	b.WriteString(publicIdentityCardFunc)
}

const publicIdentityCardFunc = `		function publicIdentityCard(id, idx) {
			var pubkeyDisplay = id.pubkey || '';
			var isPlaceholder = pubkeyDisplay === '[on_setup]';
			var pubkeyHint = isPlaceholder ? ' <span class="tg-muted">(pending setup)</span>' : '';
			return '<div class="cbi-section-node ident-card tg-section-node"><h4>Identity #' + (idx + 1) + '</h4><div class="tg-form-grid">' +
			'<label>Name</label><input class="ident-name" value="' + html(id.name || '') + '">' +
			'<label>PubKey</label><input class="ident-pubkey" value="' + html(pubkeyDisplay) + '" style="font-family:monospace;font-size:12px">' + pubkeyHint +
			'<label>Lightning Address</label><input class="ident-lightning" value="' + html(id.lightning_address || '') + '">' +
			'</div><div class="tg-actions"><button class="cbi-button cbi-button-remove remove-ident" type="button">Remove</button></div></div>';
		}

`

// ── Generated: Render functions ───────────────────────────

func jsRenderFunctions(b *strings.Builder, schema *UISchema, structs map[string]*StructDef) {
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if !sec.IsArray {
			continue
		}
		prefix := deriveCardPrefix(sec.JSONKey)

		if sec.StructName == "ProfitShareConfig" {
			writeShareRenderFunction(b)
		} else {
			writeGenericRenderFunction(b, sec, prefix)
		}
	}

	// updateShareTotal
	b.WriteString("\t\tfunction updateShareTotal() {\n")
	b.WriteString("\t\t\tvar total = 0;\n")
	b.WriteString("\t\t\tqa('.share-percent').forEach(function(el){ total += num(el.value, 0); });\n")
	b.WriteString("\t\t\tmust('#share_total').textContent = total.toFixed(2) + '%';\n")
	b.WriteString("\t\t}\n\n")

	// renderIdentities
	b.WriteString("\t\tfunction renderIdentities(data) {\n")
	b.WriteString("\t\t\tvar owned = Array.isArray(data.owned_identities) ? data.owned_identities : [];\n")
	b.WriteString("\t\t\tvar public_ = Array.isArray(data.public_identities) ? data.public_identities : [];\n\n")
	b.WriteString("\t\t\tvar ownedWrap = must('#owned_identities_rows');\n")
	b.WriteString("\t\t\townedWrap.innerHTML = '';\n")
	b.WriteString("\t\t\tif (!owned.length) {\n")
	b.WriteString("\t\t\t\townedWrap.innerHTML = '<tr><td class=\"tg-muted\">No owned identities.</td></tr>';\n")
	b.WriteString("\t\t\t} else {\n")
	b.WriteString("\t\t\t\towned.forEach(function(id) { ownedWrap.insertAdjacentHTML('beforeend', ownedIdentityRow(id)); });\n")
	b.WriteString("\t\t\t}\n\n")
	b.WriteString("\t\t\tvar publicWrap = must('#public_identities');\n")
	b.WriteString("\t\t\tpublicWrap.innerHTML = '';\n")
	b.WriteString("\t\t\tif (!public_.length) public_ = [{}];\n")
	b.WriteString("\t\t\tpublic_.forEach(function(id, idx) { publicWrap.insertAdjacentHTML('beforeend', publicIdentityCard(id, idx)); });\n")
	b.WriteString("\t\t\tqa('.remove-ident').forEach(function(btn) { btn.onclick = function() { var card = btn.closest('.ident-card'); if (card) card.remove(); }; });\n")
	b.WriteString("\t\t}\n\n")

	// updateIdentityDatalist
	b.WriteString("\t\tfunction updateIdentityDatalist(identitiesData) {\n")
	b.WriteString("\t\t\tvar dl = must('#identity_datalist');\n")
	b.WriteString("\t\t\tvar names = [];\n")
	b.WriteString("\t\t\tif (identitiesData && Array.isArray(identitiesData.public_identities)) {\n")
	b.WriteString("\t\t\t\tidentitiesData.public_identities.forEach(function(id) {\n")
	b.WriteString("\t\t\t\t\tif (id.name) names.push(id.name);\n")
	b.WriteString("\t\t\t\t});\n")
	b.WriteString("\t\t\t}\n")
	b.WriteString("\t\t\tdl.innerHTML = names.map(function(n) { return '<option value=\"' + html(n) + '\">'; }).join('');\n")
	b.WriteString("\t\t}\n\n")
}

func writeGenericRenderFunction(b *strings.Builder, sec *UISection, prefix string) {
	fmt.Fprintf(b, "\t\tfunction render%ss(list) {\n", capitalizeUpper(prefix))
	fmt.Fprintf(b, "\t\t\tvar wrap = must('#%ss');\n", prefix)
	b.WriteString("\t\t\twrap.innerHTML = '';\n")
	b.WriteString("\t\t\tif (!Array.isArray(list) || !list.length) list = [ {} ];\n")
	fmt.Fprintf(b, "\t\t\tlist.forEach(function(m, idx){ wrap.insertAdjacentHTML('beforeend', %sCard(m, idx)); });\n", prefix)
	fmt.Fprintf(b, "\t\t\tqa('.remove-%s').forEach(function(btn){ btn.onclick = function(){ var card = btn.closest('.%s-card'); if (card) card.remove(); }; });\n", prefix, prefix)
	b.WriteString("\t\t}\n\n")
}

func writeShareRenderFunction(b *strings.Builder) {
	b.WriteString("\t\tfunction renderShares(list, identitiesData) {\n")
	b.WriteString("\t\t\tvar wrap = must('#shares');\n")
	b.WriteString("\t\t\twrap.innerHTML = '';\n")
	b.WriteString("\t\t\tif (!Array.isArray(list) || !list.length) list = [ {} ];\n")
	b.WriteString("\t\t\tlist.forEach(function(s, idx){ wrap.insertAdjacentHTML('beforeend', shareCard(s, idx, identitiesData)); });\n")
	b.WriteString("\t\t\tqa('.remove-share').forEach(function(btn){ btn.onclick = function(){ var card = btn.closest('.share-card'); if (card) card.remove(); updateShareTotal(); }; });\n")
	b.WriteString("\t\t\tqa('.share-percent').forEach(function(el){ el.oninput = updateShareTotal; });\n")
	b.WriteString("\t\t\tupdateShareTotal();\n")
	b.WriteString("\t\t}\n\n")
}

// ── Generated: Collect functions ──────────────────────────

func jsCollectFunctions(b *strings.Builder, schema *UISchema, structs map[string]*StructDef) {
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if !sec.IsArray {
			continue
		}
		prefix := deriveCardPrefix(sec.JSONKey)

		if sec.StructName == "ProfitShareConfig" {
			writeShareCollectFunction(b)
		} else {
			writeGenericCollectFunction(b, sec, prefix)
		}
	}

	// collectPublicIdentities
	b.WriteString("\t\tfunction collectPublicIdentities() {\n")
	b.WriteString("\t\t\treturn qa('.ident-card').map(function(card) {\n")
	b.WriteString("\t\t\t\treturn {\n")
	b.WriteString("\t\t\t\t\tname: card.querySelector('.ident-name').value.trim(),\n")
	b.WriteString("\t\t\t\t\tpubkey: card.querySelector('.ident-pubkey').value.trim(),\n")
	b.WriteString("\t\t\t\t\tlightning_address: card.querySelector('.ident-lightning').value.trim()\n")
	b.WriteString("\t\t\t\t};\n")
	b.WriteString("\t\t\t}).filter(function(id) { return id.name.length > 0; });\n")
	b.WriteString("\t\t}\n\n")
}

func writeGenericCollectFunction(b *strings.Builder, sec *UISection, prefix string) {
	fmt.Fprintf(b, "\t\tfunction collect%ss() {\n", capitalizeUpper(prefix))
	fmt.Fprintf(b, "\t\t\treturn qa('.%s-card').map(function(card){\n", prefix)
	b.WriteString("\t\t\t\treturn {\n")
	for _, f := range sec.Fields {
		cssSel := "." + f.CSSClass
		switch f.InputType {
		case InputText:
			fmt.Fprintf(b, "\t\t\t\t\t%s: card.querySelector('%s').value.trim(),\n", f.JSONKey, cssSel)
		case InputNumber, InputNumberDuration:
			fmt.Fprintf(b, "\t\t\t\t\t%s: num(card.querySelector('%s').value, %s),\n", f.JSONKey, cssSel, f.Default)
		case InputNumberFloat:
			fmt.Fprintf(b, "\t\t\t\t\t%s: num(card.querySelector('%s').value, %s),\n", f.JSONKey, cssSel, f.Default)
		case InputCheckbox:
			fmt.Fprintf(b, "\t\t\t\t\t%s: card.querySelector('%s').checked,\n", f.JSONKey, cssSel)
		case InputTextareaLines:
			fmt.Fprintf(b, "\t\t\t\t\t%s: lines(card.querySelector('%s').value),\n", f.JSONKey, cssSel)
		case InputSelect:
			fmt.Fprintf(b, "\t\t\t\t\t%s: card.querySelector('%s').value,\n", f.JSONKey, cssSel)
		default:
			fmt.Fprintf(b, "\t\t\t\t\t%s: card.querySelector('%s').value.trim(),\n", f.JSONKey, cssSel)
		}
	}
	b.WriteString("\t\t\t\t};\n")
	// Filter: require first text field to be non-empty
	firstTextField := ""
	for _, f := range sec.Fields {
		if f.InputType == InputText {
			firstTextField = f.JSONKey
			break
		}
	}
	if firstTextField != "" {
		fmt.Fprintf(b, "\t\t\t}).filter(function(m){ return m.%s.length > 0; });\n", firstTextField)
	} else {
		b.WriteString("\t\t\t});\n")
	}
	b.WriteString("\t\t}\n\n")
}

func writeShareCollectFunction(b *strings.Builder) {
	b.WriteString("\t\tfunction collectShares() {\n")
	b.WriteString("\t\t\treturn qa('.share-card').map(function(card){\n")
	b.WriteString("\t\t\t\tvar pct = num(card.querySelector('.share-percent').value, 0);\n")
	b.WriteString("\t\t\t\treturn { identity: card.querySelector('.share-identity').value.trim(), factor: Number((pct / 100).toFixed(8)) };\n")
	b.WriteString("\t\t\t}).filter(function(s){ return s.identity.length > 0; });\n")
	b.WriteString("\t\t}\n\n")
}

// ── Generated: Form functions ─────────────────────────────

func jsFormFunctions(b *strings.Builder, schema *UISchema, structs map[string]*StructDef) {
	writePopulateForm(b, schema)
	writeFormToObject(b, schema)
	writeClientValidate(b, schema)
}

func writePopulateForm(b *strings.Builder, schema *UISchema) {
	b.WriteString("\t\tfunction populateForm(cfg) {\n")
	b.WriteString("\t\t\tcfg = cfg || {};\n")

	// Ensure arrays for card sections
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if sec.IsArray {
			fmt.Fprintf(b, "\t\t\tcfg.%s = Array.isArray(cfg.%s) ? cfg.%s : [];\n", sec.JSONKey, sec.JSONKey, sec.JSONKey)
		}
	}

	// Initialize objects for flat sections
	initialized := map[string]bool{}
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if !sec.IsArray && len(sec.Fields) > 0 {
			ensureCfgPathInit(b, sec.JSONKey, initialized)
		}
	}

	// Set top-level field values
	for _, f := range schema.TopLevelFields {
		writePopulateField(b, "cfg", f)
	}

	// Set flat section field values
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if !sec.IsArray {
			for _, f := range sec.Fields {
				writePopulateField(b, "cfg", f)
			}
		}
	}

	// Render card sections
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if sec.IsArray {
			prefix := deriveCardPrefix(sec.JSONKey)
			if sec.StructName == "ProfitShareConfig" {
				fmt.Fprintf(b, "\t\t\trenderShares(cfg.%s, state.identities);\n", sec.JSONKey)
			} else {
				fmt.Fprintf(b, "\t\t\trender%ss(cfg.%s);\n", capitalizeUpper(prefix), sec.JSONKey)
			}
		}
	}

	b.WriteString("\t\t\tmust('#raw_json').value = pretty(cfg);\n")
	b.WriteString("\t\t}\n\n")
}

func writePopulateField(b *strings.Builder, cfgVar string, f UIField) {
	switch f.InputType {
	case InputSelect:
		fmt.Fprintf(b, "\t\t\tmust('#%s').value = %s.%s || '%s';\n", f.HTMLID, cfgVar, f.JSONPath, f.SelectOpts[0])
	case InputCheckbox:
		fmt.Fprintf(b, "\t\t\tmust('#%s').checked = !!%s.%s;\n", f.HTMLID, cfgVar, f.JSONPath)
	case InputNumber:
		fmt.Fprintf(b, "\t\t\tmust('#%s').value = %s.%s || 0;\n", f.HTMLID, cfgVar, f.JSONPath)
	case InputNumberFloat:
		fmt.Fprintf(b, "\t\t\tmust('#%s').value = %s.%s || 0;\n", f.HTMLID, cfgVar, f.JSONPath)
	case InputNumberDuration:
		fmt.Fprintf(b, "\t\t\tmust('#%s').value = Math.round(num(%s.%s, 0) / 1000000000);\n", f.HTMLID, cfgVar, f.JSONPath)
	case InputTextareaLines:
		fmt.Fprintf(b, "\t\t\tmust('#%s').value = (%s.%s || []).join('\\n');\n", f.HTMLID, cfgVar, f.JSONPath)
	default:
		fmt.Fprintf(b, "\t\t\tmust('#%s').value = %s.%s || '';\n", f.HTMLID, cfgVar, f.JSONPath)
	}
}

func writeFormToObject(b *strings.Builder, schema *UISchema) {
	b.WriteString("\t\tfunction formToObject() {\n")
	b.WriteString("\t\t\tvar next = JSON.parse(JSON.stringify(state.cfg || {}));\n")

	// Initialize objects for flat sections
	initialized := map[string]bool{}
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if !sec.IsArray {
			for _, f := range sec.Fields {
				ensureNextPathInit(b, f.JSONPath, initialized)
			}
		}
	}

	// Top-level fields
	for _, f := range schema.TopLevelFields {
		writeCollectField(b, "next", f)
	}

	// Flat section fields
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if !sec.IsArray {
			for _, f := range sec.Fields {
				writeCollectField(b, "next", f)
			}
		}
	}

	// Card sections
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if sec.IsArray {
			prefix := deriveCardPrefix(sec.JSONKey)
			if sec.StructName == "ProfitShareConfig" {
				fmt.Fprintf(b, "\t\t\tnext.%s = collectShares();\n", sec.JSONKey)
			} else {
				fmt.Fprintf(b, "\t\t\tnext.%s = collect%ss();\n", sec.JSONKey, capitalizeUpper(prefix))
			}
		}
	}

	b.WriteString("\t\t\treturn next;\n")
	b.WriteString("\t\t}\n\n")
}

func writeCollectField(b *strings.Builder, nextVar string, f UIField) {
	switch f.InputType {
	case InputSelect:
		fmt.Fprintf(b, "\t\t\t%s.%s = must('#%s').value;\n", nextVar, f.JSONPath, f.HTMLID)
	case InputCheckbox:
		fmt.Fprintf(b, "\t\t\t%s.%s = must('#%s').checked;\n", nextVar, f.JSONPath, f.HTMLID)
	case InputNumber:
		fmt.Fprintf(b, "\t\t\t%s.%s = num(must('#%s').value, %s.%s || 0);\n", nextVar, f.JSONPath, f.HTMLID, nextVar, f.JSONPath)
	case InputNumberFloat:
		fmt.Fprintf(b, "\t\t\t%s.%s = Number(num(must('#%s').value, %s.%s || 0).toFixed(8));\n", nextVar, f.JSONPath, f.HTMLID, nextVar, f.JSONPath)
	case InputNumberDuration:
		fmt.Fprintf(b, "\t\t\t%s.%s = num(must('#%s').value, 0) * 1000000000;\n", nextVar, f.JSONPath, f.HTMLID)
	case InputTextareaLines:
		fmt.Fprintf(b, "\t\t\t%s.%s = lines(must('#%s').value);\n", nextVar, f.JSONPath, f.HTMLID)
	default:
		fmt.Fprintf(b, "\t\t\t%s.%s = must('#%s').value;\n", nextVar, f.JSONPath, f.HTMLID)
	}
}

func writeClientValidate(b *strings.Builder, schema *UISchema) {
	b.WriteString("\t\tfunction clientValidate(obj) {\n")
	b.WriteString("\t\t\tvar errs = [];\n")

	// Check array sections have at least one entry
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if sec.IsArray {
			fmt.Fprintf(b, "\t\t\tif (!obj.%s || !obj.%s.length) errs.push('At least one %s is required.');\n",
				sec.JSONKey, sec.JSONKey, strings.ToLower(sec.Name))
		}
	}

	// Profit share total
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if sec.StructName == "ProfitShareConfig" {
			b.WriteString("\t\t\tvar total = (obj.profit_share || []).reduce(function(acc, x){ return acc + Number(x.factor || 0); }, 0);\n")
			b.WriteString("\t\t\tif (Math.abs(total - 1.0) > 0.001) errs.push('Profit share must total 100%.');\n")
		}
	}

	// Enum validation
	for _, f := range schema.TopLevelFields {
		if f.InputType == InputSelect {
			fmt.Fprintf(b, "\t\t\tif (obj.%s !== '%s'", f.JSONKey, f.SelectOpts[0])
			for _, opt := range f.SelectOpts[1:] {
				fmt.Fprintf(b, " && obj.%s !== '%s'", f.JSONKey, opt)
			}
			fmt.Fprintf(b, ") errs.push('%s must be one of: %s.');\n", f.Label, strings.Join(f.SelectOpts, ", "))
		}
	}

	b.WriteString("\t\t\treturn errs;\n")
	b.WriteString("\t\t}\n\n")
}

func ensureCfgPathInit(b *strings.Builder, path string, initialized map[string]bool) {
	parts := strings.Split(path, ".")
	for i := 1; i <= len(parts); i++ {
		prefix := strings.Join(parts[:i], ".")
		if !initialized[prefix] {
			fmt.Fprintf(b, "\t\t\tcfg.%s = cfg.%s || {};\n", prefix, prefix)
			initialized[prefix] = true
		}
	}
}

func ensureNextPathInit(b *strings.Builder, path string, initialized map[string]bool) {
	parts := strings.Split(path, ".")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], ".")
		if !initialized[prefix] {
			fmt.Fprintf(b, "\t\t\tnext.%s = next.%s || {};\n", prefix, prefix)
			initialized[prefix] = true
		}
	}
}

// ── Static: API / confirm ─────────────────────────────────

func jsAPIFunctions(b *strings.Builder) {
	b.WriteString(apiFuncs)
}

const apiFuncs = `		function api(action, body) {
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
`

func jsConfirmFunction(b *strings.Builder) {
	b.WriteString(confirmFunc)
}

const confirmFunc = `		function confirm(message, onConfirm) {
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
`

// ── Static: Dashboard functions ───────────────────────────

func jsDashboardFunctions(b *strings.Builder) {
	b.WriteString(dashboardFuncs)
}

const dashboardFuncs = `		function applyDashboard(data) {
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
`

// ── Static: Wallet ────────────────────────────────────────

func jsWalletFunctions(b *strings.Builder) {
	b.WriteString(walletFuncs)
}

const walletFuncs = `		function doWalletFund() {
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
`

// ── Static: Network functions ─────────────────────────────

func jsNetworkFunctions(b *strings.Builder) {
	b.WriteString(networkFuncs)
}

const networkFuncs = `		function renderNetworkStatus(data) {
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
`

// ── Static: Config save/validate ─────────────────────────

func jsConfigSaveFunctions(b *strings.Builder) {
	b.WriteString(configSaveFuncs)
}

const configSaveFuncs = `		function validateText(text) {
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
`

// ── Static: Identities save ───────────────────────────────

func jsIdentitiesSaveFunction(b *strings.Builder) {
	b.WriteString(identitiesSaveFunc)
}

const identitiesSaveFunc = `		function doSaveIdentities() {
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
`

// ── Static: Tab switching / polling / init ────────────────

func jsTabSwitching(b *strings.Builder) {
	b.WriteString(tabSwitchingFunc)
}

const tabSwitchingFunc = `		function setActiveTab(name) {
			state.activeTab = name;
			['dashboard', 'network', 'config', 'identities'].forEach(function(tab) {
				must('#pane_' + tab).classList.toggle('active', tab === name);
				must('#tab_' + tab).className = tab === name ? 'cbi-button cbi-button-action' : 'cbi-button';
			});
			if (name === 'network') refreshNetworkStatus();
		}
`

func jsPolling(b *strings.Builder) {
	b.WriteString(pollingFunc)
}

const pollingFunc = `		function startPolling() {
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
`

func jsBindHandlers(b *strings.Builder, schema *UISchema, structs map[string]*StructDef) {
	b.WriteString("\t\tfunction bindHandlers() {\n")

	// Tab buttons
	b.WriteString("\t\t\tmust('#tab_dashboard').onclick = function() { setActiveTab('dashboard'); };\n")
	b.WriteString("\t\t\tmust('#tab_network').onclick = function() { setActiveTab('network'); };\n")
	b.WriteString("\t\t\tmust('#tab_config').onclick = function() { setActiveTab('config'); };\n")
	b.WriteString("\t\t\tmust('#tab_identities').onclick = function() { setActiveTab('identities'); };\n\n")

	// Dashboard
	b.WriteString("\t\t\tmust('#btn_fund').onclick = function() { doWalletFund(); };\n\n")

	// Network
	b.WriteString("\t\t\tmust('#btn_toggle_pw').onclick = function() { state.netPwVisible = !state.netPwVisible; updateNetPwDisplay(); };\n")
	b.WriteString("\t\t\tmust('#btn_net_enable').onclick = function() { doNetworkEnable(); };\n")
	b.WriteString("\t\t\tmust('#btn_net_disable').onclick = function() { doNetworkDisable(); };\n")
	b.WriteString("\t\t\tmust('#btn_net_rename').onclick = function() { doNetworkRename(); };\n")
	b.WriteString("\t\t\tmust('#btn_net_setpw').onclick = function() { doNetworkSetPassword(false); };\n")
	b.WriteString("\t\t\tmust('#btn_net_genpw').onclick = function() { doNetworkSetPassword(true); };\n\n")

	// Card add buttons
	for i := range schema.Sections {
		sec := &schema.Sections[i]
		if sec.IsArray {
			prefix := deriveCardPrefix(sec.JSONKey)
			if sec.StructName == "ProfitShareConfig" {
				fmt.Fprintf(b, "\t\t\tmust('#add_%s').onclick = function() { var list = collectShares(); list.push({}); renderShares(list, state.identities); };\n", prefix)
			} else {
				fmt.Fprintf(b, "\t\t\tmust('#add_%s').onclick = function() { var list = collect%ss(); list.push({}); render%ss(list); };\n", prefix, capitalizeUpper(prefix), capitalizeUpper(prefix))
			}
		}
	}

	// Identity buttons
	b.WriteString("\t\t\tmust('#btn_add_ident').onclick = function() {\n")
	b.WriteString("\t\t\t\tvar data = state.identities || {};\n")
	b.WriteString("\t\t\t\tvar list = Array.isArray(data.public_identities) ? collectPublicIdentities() : [];\n")
	b.WriteString("\t\t\t\tlist.push({});\n")
	b.WriteString("\t\t\t\tdata.public_identities = list;\n")
	b.WriteString("\t\t\t\trenderIdentities(data);\n")
	b.WriteString("\t\t\t};\n")
	b.WriteString("\t\t\tmust('#btn_save_identities').onclick = function() { doSaveIdentities(); };\n\n")

	// Config action buttons
	b.WriteString("\t\t\tmust('#validate_form').onclick = function() {\n")
	b.WriteString("\t\t\t\ttry {\n")
	b.WriteString("\t\t\t\t\tvar obj = formToObject();\n")
	b.WriteString("\t\t\t\t\tvar errs = clientValidate(obj);\n")
	b.WriteString("\t\t\t\t\tif (errs.length) return setMsg(errs);\n")
	b.WriteString("\t\t\t\t\tvalidateText(pretty(obj));\n")
	b.WriteString("\t\t\t\t} catch (e) { setMsg(['Validate failed: ' + e.message]); }\n")
	b.WriteString("\t\t\t};\n")
	b.WriteString("\t\t\tmust('#save_form').onclick = function() {\n")
	b.WriteString("\t\t\t\ttry {\n")
	b.WriteString("\t\t\t\t\tvar obj = formToObject();\n")
	b.WriteString("\t\t\t\t\tvar errs = clientValidate(obj);\n")
	b.WriteString("\t\t\t\t\tif (errs.length) return setMsg(errs);\n")
	b.WriteString("\t\t\t\t\tsaveText(pretty(obj));\n")
	b.WriteString("\t\t\t\t} catch (e) { setMsg(['Save failed: ' + e.message]); }\n")
	b.WriteString("\t\t\t};\n")
	b.WriteString("\t\t\tmust('#json_to_forms').onclick = function() {\n")
	b.WriteString("\t\t\t\ttry {\n")
	b.WriteString("\t\t\t\t\tvar obj = parseJson(must('#raw_json').value);\n")
	b.WriteString("\t\t\t\t\tstate.cfg = obj;\n")
	b.WriteString("\t\t\t\t\tpopulateForm(obj);\n")
	b.WriteString("\t\t\t\t\tsetMsg(['Loaded form values from JSON.']);\n")
	b.WriteString("\t\t\t\t} catch (e) { setMsg(['JSON → Forms failed: ' + e.message]); }\n")
	b.WriteString("\t\t\t};\n")
	b.WriteString("\t\t\tmust('#validate_json').onclick = function() { validateText(must('#raw_json').value); };\n")
	b.WriteString("\t\t\tmust('#save_json').onclick = function() { saveText(must('#raw_json').value); };\n")
	b.WriteString("\t\t}\n\n")
}

func jsInit(b *strings.Builder) {
	b.WriteString("\t\ttry {\n")
	b.WriteString("\t\t\tbindHandlers();\n")
	b.WriteString("\t\t\tstartPolling();\n")
	b.WriteString("\t\t\trefreshDashboard();\n")
	b.WriteString("\t\t} catch (e) {\n")
	b.WriteString("\t\t\troot.innerHTML = '<div class=\"cbi-map\"><h2>TollGate</h2><pre>UI error: ' + html(String(e.message || e)) + '</pre></div>';\n")
	b.WriteString("\t\t}\n\n")
	b.WriteString("\t\treturn root;\n")
}
