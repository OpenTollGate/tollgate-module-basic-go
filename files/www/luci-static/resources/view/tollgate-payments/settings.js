'use strict';
'require view';
'require fs';
'require poll';
'require ui';

var HELPER = '/usr/libexec/tollgate-luci-helper';
var CLI = '/usr/bin/tollgate';
var CONFIG = '/etc/tollgate/config.json';
var IDENTITIES = '/etc/tollgate/identities.json';

// cli() returns raw text; callers parse with regex. If CLI output format
// changes, the regex patterns in formatBalance() and waitForOverview() will
// need updating. A future --json flag on the CLI would make this more robust.
function cli() {
	var args = [];
	for (var i = 0; i < arguments.length; i++) args.push(arguments[i]);
	return fs.exec_direct(CLI, args);
}

function helper() {
	var args = [];
	for (var i = 0; i < arguments.length; i++) args.push(arguments[i]);
	return fs.exec_direct(HELPER, args, 'json');
}

function saveJsonFile(path, data) {
	var ts = new Date().toISOString().replace(/[^0-9T]/g, '').slice(0, 15);
	return fs.exec_direct('/bin/cp', [path, path + '.bak.' + ts]).catch(function() {}).then(function() {
		return fs.write(path, JSON.stringify(data, null, 2) + '\n');
	});
}

function badge(label, bg, fg) {
	return E('span', { 'class': 'ifacebadge', 'style': 'background:' + bg + ';color:' + fg + ';padding:2px 8px;border-radius:3px;font-size:12px;font-weight:600' }, label);
}

function formatBalance(text) {
	var match = String(text || '').match(/(\d+)\s*sats/i);
	return match ? match[1] + ' sats' : (text || '—').trim() || '—';
}

function humanStepSize(bytes) {
	if (bytes >= 1073741824) return (bytes / 1073741824).toFixed(1) + ' GiB';
	if (bytes >= 1048576) return (bytes / 1048576).toFixed(1) + ' MiB';
	if (bytes >= 1024) return (bytes / 1024).toFixed(1) + ' KiB';
	return bytes + ' B';
}

function statusBadge(text) {
	if (/running|active|uptime/i.test(text || ''))
		return badge('Running', '#dff0d8', '#3c763d');
	if (/stopped|not running|inactive/i.test(text || ''))
		return badge('Stopped', '#f2dede', '#a94442');
	return badge('Unknown', '#fcf8e3', '#8a6d3b');
}

return view.extend({
	load: function() {
		return Promise.all([
			cli('wallet', 'balance').catch(function() { return ''; }),
			cli('version').catch(function() { return ''; }),
			cli('status').catch(function() { return ''; })
		]);
	},

	render: function(data) {
		var balance = data[0] || '';
		var version = data[1] || '';
		var statusText = data[2] || '';

		var activeTab = 'overview';
		var tabContent = E('div');
		var pollFailCount = 0;

		function q(id) { return document.getElementById(id); }

		function setTab(name) {
			activeTab = name;
			var tabs = ['overview', 'wallet', 'network', 'config', 'logs', 'advanced'];
			tabs.forEach(function(t) {
				var btn = q('tab_' + t);
				if (btn) btn.className = t === name ? 'cbi-button cbi-button-action' : 'cbi-button';
			});
			if (name === 'overview') renderOverview();
			else if (name === 'wallet') renderWallet();
			else if (name === 'network') renderNetwork();
			else if (name === 'config') renderConfig();
			else if (name === 'logs') renderLogs();
			else if (name === 'advanced') renderAdvanced();
		}

		function renderOverview() {
			tabContent.innerHTML = '';
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Service Status')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Wallet Balance')),
					E('div', { 'class': 'cbi-value-field', 'id': 'ov_balance', 'style': 'font-size:24px;font-weight:700' }, formatBalance(balance))
				]),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Service')),
					E('div', { 'class': 'cbi-value-field', 'id': 'ov_status' }, statusBadge(statusText))
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-apply', 'click': function() { svcControl('start'); } }, _('Start')),
					E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() {
						ui.showModal(_('Stop Services'), [
							E('p', _('This will stop TollGate and NoDogSplash. Users will lose connectivity.')),
							E('div', { 'class': 'right' }, [
								E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')), ' ',
								E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() { ui.hideModal(); svcControl('stop'); } }, _('Confirm'))
							])
						]);
					}}, _('Stop')),
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': function() { svcControl('restart'); } }, _('Restart'))
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Version')),
				E('pre', { 'id': 'ov_version', 'style': 'white-space:pre-wrap;font-family:monospace;font-size:13px;margin:0' }, (version || '—').trim())
			]));
		}

		function renderWallet() {
			tabContent.innerHTML = '';
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Wallet Balance')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Total Balance')),
					E('div', { 'class': 'cbi-value-field', 'id': 'wl_balance', 'style': 'font-size:24px;font-weight:700' }, 'Loading…')
				]),
				E('pre', { 'id': 'wl_info', 'style': 'white-space:pre-wrap;font-family:monospace;font-size:13px;max-height:12rem;overflow:auto;margin:0' }, 'Loading…')
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Fund Wallet')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title', 'for': 'wl_token' }, _('Cashu Token')),
					E('div', { 'class': 'cbi-value-field' }, [
						E('input', { 'type': 'password', 'id': 'wl_token', 'class': 'cbi-input-password', 'placeholder': _('Paste your Cashu ecash token'), 'style': 'max-width:400px;width:100%' })
					]),
					E('div', { 'class': 'cbi-value-description' }, _('Paste a Cashu token to add funds. The token will be consumed.'))
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-apply', 'id': 'wl_fund_btn', 'click': walletFund }, _('Fund Wallet')),
					' ', E('span', { 'id': 'wl_fund_state', 'style': 'font-size:13px;opacity:.7' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Drain Wallet')),
				E('p', { 'style': 'font-size:13px;opacity:.7' }, _('Convert all wallet funds to Cashu tokens. Copy the tokens to a safe place.')),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-remove', 'click': walletDrain }, _('Drain All Funds')),
					' ', E('span', { 'id': 'wl_drain_state', 'style': 'font-size:13px;opacity:.7' })
				]),
				E('pre', { 'id': 'wl_drain_result', 'style': 'display:none;white-space:pre-wrap;font-family:monospace;font-size:13px;max-height:16rem;overflow:auto;margin-top:8px' })
			]));

			cli('wallet', 'balance').then(function(b) { var el = q('wl_balance'); if (el) el.textContent = formatBalance(b); }).catch(function() {});
			cli('wallet', 'info').then(function(t) { var el = q('wl_info'); if (el) el.textContent = (t || 'No info.').trim(); }).catch(function() {});
		}

		function renderNetwork() {
			tabContent.innerHTML = '';
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Private WiFi Network')),
				E('div', { 'id': 'nw_loading', 'style': 'font-size:13px;opacity:.7' }, 'Loading…')
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Rename Network')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title', 'for': 'nw_new_ssid' }, _('New SSID')),
					E('div', { 'class': 'cbi-value-field' }, [
						E('input', { 'type': 'text', 'id': 'nw_new_ssid', 'class': 'cbi-input-text', 'placeholder': _('Enter new network name'), 'style': 'max-width:400px;width:100%' })
					])
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': wifiRename }, _('Rename')),
					' ', E('span', { 'id': 'nw_rename_state', 'style': 'font-size:13px;opacity:.7' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Change Password')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title', 'for': 'nw_new_pw' }, _('New Password')),
					E('div', { 'class': 'cbi-value-field' }, [
						E('input', { 'type': 'password', 'id': 'nw_new_pw', 'class': 'cbi-input-password', 'placeholder': _('Leave empty to generate random'), 'style': 'max-width:400px;width:100%' })
					]),
					E('div', { 'class': 'cbi-value-description' }, _('Leave empty to auto-generate a memorable password.'))
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': wifiPassword }, _('Change Password')),
					' ', E('span', { 'id': 'nw_pw_state', 'style': 'font-size:13px;opacity:.7' })
				])
			]));

			helper('network', 'private', 'status').then(function(resp) {
				var d = (resp && resp.data) || {};
				var el = q('nw_loading');
				if (!el) return;
				el.innerHTML = '';
				el.style.opacity = '1';
				var enabled = d.enabled;
				el.appendChild(E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Status')),
					E('div', { 'class': 'cbi-value-field' }, enabled
						? badge('Enabled', '#dff0d8', '#3c763d')
						: badge('Disabled', '#f2dede', '#a94442'))
				]));
				el.appendChild(E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('SSID')),
					E('div', { 'class': 'cbi-value-field', 'style': 'font-size:18px;font-weight:600' }, d.ssid || '—')
				]));
				el.appendChild(E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Password')),
					E('div', { 'class': 'cbi-value-field', 'style': 'font-size:14px;font-family:monospace' }, d.password || '—')
				]));
				el.appendChild(E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-apply', 'click': function() {
						helper('network', 'private', 'enable').then(function() { setTab('network'); });
					}}, _('Enable')),
					E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() {
						ui.showModal(_('Disable Private WiFi'), [
							E('p', _('Disabling the private network may lock you out of the router.')),
							E('div', { 'class': 'right' }, [
								E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')), ' ',
								E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() {
									ui.hideModal();
									helper('network', 'private', 'disable').then(function() { setTab('network'); });
								}}, _('Confirm'))
							])
						]);
					}}, _('Disable'))
				]));
			}).catch(function(err) {
				var el = q('nw_loading');
				if (el) el.textContent = 'Failed to load: ' + err;
			});
		}

		function renderConfig() {
			tabContent.innerHTML = '';
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Pricing')),
				E('div', { 'id': 'cfg_content', 'style': 'font-size:13px;opacity:.7' }, 'Loading…')
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Accepted Mints')),
				E('pre', { 'id': 'cfg_mints', 'style': 'white-space:pre-wrap;font-family:monospace;font-size:13px;margin:0' }, 'Loading…')
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Profit Share')),
				E('pre', { 'id': 'cfg_profit', 'style': 'white-space:pre-wrap;font-family:monospace;font-size:13px;margin:0' }, 'Loading…')
			]));

			fs.read_direct(CONFIG, 'json').then(function(config) {
				var mints = config.accepted_mints || [];
				var profit = config.profit_share || [];
				var el = q('cfg_content');
				if (!el) return;
				if (mints.length > 0) {
					var m = mints[0];
					el.innerHTML = '';
					el.style.opacity = '1';
					el.appendChild(E('div', { 'class': 'cbi-value' }, [
						E('label', { 'class': 'cbi-value-title' }, _('Price per Step')),
						E('div', { 'class': 'cbi-value-field', 'style': 'font-size:20px;font-weight:700' }, (m.price_per_step || '—') + ' ' + (m.price_unit || 'sats'))
					]));
					el.appendChild(E('div', { 'class': 'cbi-value' }, [
						E('label', { 'class': 'cbi-value-title' }, _('Step Size')),
						E('div', { 'class': 'cbi-value-field', 'style': 'font-size:20px;font-weight:700' },
							config.metric === 'milliseconds' ? ((config.step_size || 0) / 1000) + 's' : humanStepSize(config.step_size || 0))
					]));
					el.appendChild(E('div', { 'class': 'cbi-value' }, [
						E('label', { 'class': 'cbi-value-title' }, _('Metric')),
						E('div', { 'class': 'cbi-value-field', 'style': 'font-size:20px;font-weight:700' }, config.metric || '—')
					]));
				} else {
					el.textContent = 'No configuration found.';
				}
				var mintsEl = q('cfg_mints');
				if (mintsEl) mintsEl.textContent = JSON.stringify(mints, null, 2);
				var profitEl = q('cfg_profit');
				if (profitEl) profitEl.textContent = JSON.stringify(profit, null, 2);
			}).catch(function(err) {
				var el = q('cfg_content');
				if (el) el.textContent = 'Failed: ' + err;
			});
		}

		function renderLogs() {
			tabContent.innerHTML = '';
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Service Logs')),
				E('p', { 'style': 'font-size:13px;opacity:.7' }, _('Recent tollgate-wrt log lines. Auto-refreshes while this tab is open.')),
				E('pre', { 'id': 'logs_box', 'style': 'white-space:pre-wrap;font-family:monospace;font-size:13px;max-height:24rem;overflow:auto;margin:0;background:var(--background-color,#f5f5f5);padding:.5rem;border-radius:3px' }, 'Loading…')
			]));
			fs.exec_direct('/sbin/logread', ['-e', 'tollgate-wrt', '-l', '300']).then(function(t) {
				var el = q('logs_box');
				if (el) el.textContent = (t || 'No log lines.').trim();
			}).catch(function() {
				var el = q('logs_box');
				if (el) el.textContent = 'No log lines.';
			});
		}

		function renderAdvanced() {
			tabContent.innerHTML = '';
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Raw JSON Editor')),
				E('p', { 'style': 'font-size:13px;opacity:.7' }, _('Edit configuration files directly. Changes take effect after saving.')),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button', 'id': 'reload_files_btn', 'click': loadFiles }, _('Reload both files')),
					' ', E('span', { 'id': 'files_state', 'style': 'font-size:13px;opacity:.7' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', 'config.json'),
				E('textarea', { 'id': 'config_editor', 'class': 'cbi-input-textarea', 'style': 'width:100%;min-height:18rem;font-family:monospace;font-size:12px;line-height:1.45;resize:vertical', 'spellcheck': 'false' }),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-action', 'click': function() { validateEditor(CONFIG, 'config_editor', 'config_state'); } }, _('Validate')),
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': function() { saveEditor(CONFIG, 'config_editor', 'config_state'); } }, _('Save config.json')),
					' ', E('span', { 'id': 'config_state', 'style': 'font-size:13px' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', 'identities.json'),
				E('textarea', { 'id': 'identities_editor', 'class': 'cbi-input-textarea', 'style': 'width:100%;min-height:18rem;font-family:monospace;font-size:12px;line-height:1.45;resize:vertical', 'spellcheck': 'false' }),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-action', 'click': function() { validateEditor(IDENTITIES, 'identities_editor', 'identities_state'); } }, _('Validate')),
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': function() { saveEditor(IDENTITIES, 'identities_editor', 'identities_state'); } }, _('Save identities.json')),
					' ', E('span', { 'id': 'identities_state', 'style': 'font-size:13px' })
				])
			]));
			loadFiles();
		}

		function loadFiles() {
			var stateEl = q('files_state');
			if (stateEl) stateEl.textContent = 'Loading…';
			Promise.all([
				fs.read_direct(CONFIG, 'json').catch(function() { return null; }),
				fs.read_direct(IDENTITIES, 'json').catch(function() { return null; })
			]).then(function(results) {
				var cfgEl = q('config_editor');
				if (cfgEl) cfgEl.value = results[0] ? JSON.stringify(results[0], null, 2) + '\n' : '// config.json not found\n';
				var idsEl = q('identities_editor');
				if (idsEl) idsEl.value = results[1] ? JSON.stringify(results[1], null, 2) + '\n' : '// identities.json not found\n';
				if (stateEl) stateEl.textContent = 'Loaded ' + new Date().toLocaleTimeString();
			});
		}

		function validateEditor(path, editorId, stateId) {
			var text = (q(editorId) || {}).value || '';
			try { JSON.parse(text); } catch(e) {
				var el = q(stateId);
				if (el) { el.textContent = 'Invalid JSON: ' + e.message; el.style.color = '#d9534f'; }
				return;
			}
			var el = q(stateId);
			if (el) { el.textContent = 'Valid JSON'; el.style.color = '#5cb85c'; }
		}

		function saveEditor(path, editorId, stateId) {
			var text = (q(editorId) || {}).value || '';
			try { JSON.parse(text); } catch(e) {
				var el = q(stateId);
				if (el) { el.textContent = 'Invalid JSON: ' + e.message; el.style.color = '#d9534f'; }
				return;
			}
			var stateEl = q(stateId);
			if (stateEl) { stateEl.textContent = 'Saving…'; stateEl.style.color = ''; }
			var data = JSON.parse(text);
			saveJsonFile(path, data).then(function() {
				if (stateEl) { stateEl.textContent = 'Saved'; stateEl.style.color = '#5cb85c'; }
			}).catch(function(err) {
				if (stateEl) { stateEl.textContent = 'Save failed: ' + err; stateEl.style.color = '#d9534f'; }
			});
		}

		function svcControl(action) {
			var svcCmds = {
				start: [['/etc/init.d/tollgate-wrt', ['start']], ['/etc/init.d/nodogsplash', ['start']]],
				stop: [['/etc/init.d/tollgate-wrt', ['stop']], ['/etc/init.d/nodogsplash', ['stop']]],
				restart: [['/etc/init.d/nodogsplash', ['restart']], ['/etc/init.d/tollgate-wrt', ['restart']]]
			};
			var cmds = svcCmds[action];
			if (!cmds) return;
			Promise.all(cmds.map(function(c) { return fs.exec_direct(c[0], c[1]); })).then(function() {
				setTimeout(function() { refreshOverview(); }, 3000);
			});
		}

		function walletFund() {
			var tokenEl = q('wl_token');
			var stateEl = q('wl_fund_state');
			var btnEl = q('wl_fund_btn');
			var token = (tokenEl || {}).value || '';
			if (!token.trim()) {
				if (stateEl) stateEl.textContent = 'Enter a token first.';
				return;
			}
			if (btnEl) btnEl.disabled = true;
			if (stateEl) stateEl.textContent = 'Funding…';
			helper('wallet', 'fund', token.trim()).then(function(resp) {
				if (resp && resp.success) {
					if (stateEl) stateEl.textContent = 'Funded successfully.';
					if (tokenEl) tokenEl.value = '';
					cli('wallet', 'balance').then(function(b) {
						var el = q('wl_balance');
						if (el) el.textContent = formatBalance(b);
					});
				} else {
					if (stateEl) stateEl.textContent = 'Failed: ' + ((resp && resp.error) || 'Unknown error');
				}
			}).catch(function(err) {
				if (stateEl) stateEl.textContent = 'Error: ' + err;
			}).finally(function() {
				if (btnEl) btnEl.disabled = false;
			});
		}

		function walletDrain() {
			ui.showModal(_('Drain Wallet'), [
				E('p', _('This will convert ALL wallet funds to Cashu tokens. Copy them to a safe place — they will no longer be in the wallet.')),
				E('div', { 'class': 'right' }, [
					E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')), ' ',
					E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() {
						ui.hideModal();
						var stateEl = q('wl_drain_state');
						if (stateEl) stateEl.textContent = 'Draining…';
						helper('wallet', 'drain', 'cashu').then(function(resp) {
							var d = (resp && resp.data) || {};
							var tokens = d.tokens || [];
							var lines = ['Total drained: ' + (d.total_sats || 0) + ' sats', ''];
							tokens.forEach(function(t, i) {
								lines.push('Token ' + (i + 1) + ': ' + (t.balance_sats || 0) + ' sats (' + (t.mint_url || '') + ')');
								if (t.token) lines.push(t.token);
								lines.push('');
							});
							var resultEl = q('wl_drain_result');
							if (resultEl) { resultEl.textContent = lines.join('\n'); resultEl.style.display = 'block'; }
							if (stateEl) stateEl.textContent = 'Drained.';
							cli('wallet', 'balance').then(function(b) {
								var el = q('wl_balance');
								if (el) el.textContent = formatBalance(b);
							});
						}).catch(function(err) {
							if (stateEl) stateEl.textContent = 'Error: ' + err;
						});
					}}, _('Confirm'))
				])
			]);
		}

		function wifiRename() {
			var ssidEl = q('nw_new_ssid');
			var stateEl = q('nw_rename_state');
			var ssid = (ssidEl || {}).value || '';
			if (!ssid.trim()) { if (stateEl) stateEl.textContent = 'Enter a new SSID.'; return; }
			if (stateEl) stateEl.textContent = 'Renaming…';
			helper('network', 'private', 'rename', ssid.trim()).then(function(resp) {
				if (resp && resp.success) {
					if (stateEl) stateEl.textContent = 'Renamed to ' + ssid;
					if (ssidEl) ssidEl.value = '';
					setTab('network');
				} else {
					if (stateEl) stateEl.textContent = 'Failed: ' + ((resp && resp.error) || 'Unknown error');
				}
			}).catch(function(err) {
				if (stateEl) stateEl.textContent = 'Error: ' + err;
			});
		}

		function wifiPassword() {
			var pwEl = q('nw_new_pw');
			var stateEl = q('nw_pw_state');
			var pw = (pwEl || {}).value || '';
			if (stateEl) stateEl.textContent = 'Changing…';
			var args = pw.trim() ? ['network', 'private', 'set-password', pw.trim()] : ['network', 'private', 'set-password'];
			helper.apply(null, args).then(function(resp) {
				if (resp && resp.success) {
					if (stateEl) stateEl.textContent = 'Password changed.';
					if (pwEl) pwEl.value = '';
					setTab('network');
				} else {
					if (stateEl) stateEl.textContent = 'Failed: ' + ((resp && resp.error) || 'Unknown error');
				}
			}).catch(function(err) {
				if (stateEl) stateEl.textContent = 'Error: ' + err;
			});
		}

		function refreshOverview() {
			cli('wallet', 'balance').then(function(b) {
				pollFailCount = 0;
				clearPollWarning();
				var el = q('ov_balance');
				if (el) el.textContent = formatBalance(b);
				var wlEl = q('wl_balance');
				if (wlEl) wlEl.textContent = formatBalance(b);
			}).catch(function() { pollFailCount++; showPollWarning(); });
			cli('status').then(function(s) {
				var el = q('ov_status');
				if (el) { el.innerHTML = ''; el.appendChild(statusBadge(s)); }
			}).catch(function() {});
		}

		function showPollWarning() {
			if (pollFailCount < 3) return;
			var existing = q('poll_warning');
			if (existing) return;
			var header = tabContent.parentNode;
			if (!header) return;
			var warn = E('div', { 'id': 'poll_warning', 'class': 'alert-message warning', 'style': 'background:#fcf8e3;color:#8a6d3b;padding:8px 12px;border-radius:3px;margin-bottom:8px;font-size:13px' }, _('Connection to service lost. Retrying…'));
			header.insertBefore(warn, tabContent);
		}

		function clearPollWarning() {
			var warn = q('poll_warning');
			if (warn) warn.parentNode.removeChild(warn);
		}

		poll.add(function() {
			if (document.hidden) return;
			if (activeTab === 'overview') return refreshOverview();
			if (activeTab === 'logs') {
				return fs.exec_direct('/sbin/logread', ['-e', 'tollgate-wrt', '-l', '300']).then(function(t) {
					pollFailCount = 0;
					clearPollWarning();
					var el = q('logs_box');
					if (el) el.textContent = (t || 'No log lines.').trim();
				}).catch(function() { pollFailCount++; showPollWarning(); });
			}
		}, 5);

		var viewEl = E('div', { 'class': 'cbi-map' }, [
			E('h2', { 'name': 'content' }, 'TollGate'),
			E('div', { 'class': 'cbi-map-descr' }, _('Manage your TollGate captive portal payment gateway.')),
			E('div', { 'style': 'display:flex;gap:6px;flex-wrap:wrap;margin:0 0 1.2rem 0' }, [
				E('button', { 'id': 'tab_overview', 'class': 'cbi-button cbi-button-action', 'click': function() { setTab('overview'); } }, _('Overview')),
				E('button', { 'id': 'tab_wallet', 'class': 'cbi-button', 'click': function() { setTab('wallet'); } }, _('Wallet')),
				E('button', { 'id': 'tab_network', 'class': 'cbi-button', 'click': function() { setTab('network'); } }, _('Network')),
				E('button', { 'id': 'tab_config', 'class': 'cbi-button', 'click': function() { setTab('config'); } }, _('Configuration')),
				E('button', { 'id': 'tab_logs', 'class': 'cbi-button', 'click': function() { setTab('logs'); } }, _('Logs')),
				E('button', { 'id': 'tab_advanced', 'class': 'cbi-button', 'click': function() { setTab('advanced'); } }, _('Advanced'))
			]),
			tabContent
		]);

		renderOverview();

		return viewEl;
	},

	handleSave: null,
	handleSaveApply: null,
	handleReset: null
});
