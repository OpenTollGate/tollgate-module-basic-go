'use strict';
'require view';
'require fs';
'require poll';
'require ui';

var CLI = '/usr/bin/tollgate';

function cliJson() {
	var args = [];
	for (var i = 0; i < arguments.length; i++) args.push(arguments[i]);
	args.push('--json');
	return fs.exec_direct(CLI, args, 'json').then(function(resp) {
		if (resp && !resp.success && resp.error) return Promise.reject(resp.error);
		return resp;
	});
}

function clearNode(node) { while (node.firstChild) node.removeChild(node.firstChild); }

function badge(label, cls) {
	return E('span', { 'class': 'ifacebadge tg-badge ' + (cls || '') }, label);
}

function statusBadge(running) {
	if (running === true) return badge('Running', 'tg-badge-running');
	if (running === false) return badge('Stopped', 'tg-badge-stopped');
	return badge('Unknown', 'tg-badge-unknown');
}

function stateSpan(id, text, cls) {
	var el = document.getElementById(id);
	if (!el) return;
	el.textContent = text || '';
	el.className = el.className.replace(/\btg-state-\S+/g, '').trim();
	if (cls) el.classList.add(cls);
}

function setSvcButtons(disabled) {
	['ov_btn_start', 'ov_btn_stop', 'ov_btn_restart'].forEach(function(id) {
		var el = document.getElementById(id);
		if (el) el.disabled = disabled;
	});
}

function copyToClipboard(text) {
	if (navigator.clipboard && navigator.clipboard.writeText) return navigator.clipboard.writeText(text);
	var ta = document.createElement('textarea');
	ta.value = text;
	ta.style.position = 'fixed';
	ta.style.left = '-9999px';
	document.body.appendChild(ta);
	ta.select();
	document.execCommand('copy');
	document.body.removeChild(ta);
	return Promise.resolve();
}

function saveJsonViaService(type, jsonStr) {
	return cliJson('config', type === 'config' ? 'save' : 'save-identities', jsonStr);
}

return view.extend({
	load: function() {
		return Promise.all([
			cliJson('wallet', 'balance').catch(function() { return null; }),
			cliJson('status').catch(function() { return null; }),
			cliJson('version').catch(function() { return null; }),
			cliJson('config', 'schema').catch(function() { return null; })
		]);
	},

	render: function(data) {
		var balanceResp = data[0];
		var statusResp = data[1];
		var versionResp = data[2];
		var schemaResp = data[3];

		var activeTab = 'overview';
		var tabContent = E('div');
		var pollFailCount = 0;
		var cachedSchema = (schemaResp && schemaResp.data) || null;

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
			clearNode(tabContent);
			var balanceSats = '—';
			var statusData = null;
			var versionData = null;

			if (balanceResp && balanceResp.data) {
				balanceSats = (balanceResp.data.balance_sats || 0) + ' sats';
			}
			if (statusResp && statusResp.data) {
				statusData = statusResp.data;
			}
			if (versionResp && versionResp.data) {
				versionData = versionResp.data;
			}

			var statusRunning = statusData ? statusData.running : false;

			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Service Status')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Wallet Balance')),
					E('div', { 'class': 'cbi-value-field tg-balance', 'id': 'ov_balance' }, balanceSats)
				]),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Service')),
					E('div', { 'class': 'cbi-value-field', 'id': 'ov_status' }, statusBadge(statusRunning))
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-apply', 'id': 'ov_btn_start', 'click': function() { svcControl('start'); } }, _('Start')),
					E('button', { 'class': 'cbi-button cbi-button-remove', 'id': 'ov_btn_stop', 'click': function() {
						ui.showModal(_('Stop Services'), [
							E('p', _('This will stop TollGate and NoDogSplash. Users will lose connectivity.')),
							E('div', { 'class': 'right' }, [
								E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')), ' ',
								E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() { ui.hideModal(); svcControl('stop'); } }, _('Confirm'))
							])
						]);
					}}, _('Stop')),
					E('button', { 'class': 'cbi-button cbi-button-save', 'id': 'ov_btn_restart', 'click': function() { svcControl('restart'); } }, _('Restart'))
				])
			]));

			var versionLines = [];
			if (versionData) {
				if (versionData.version) versionLines.push('Version: ' + versionData.version);
				if (versionData.commit) versionLines.push('Commit: ' + versionData.commit);
				if (versionData.build_time) versionLines.push('Built: ' + versionData.build_time);
				if (versionData.go_version) versionLines.push('Go: ' + versionData.go_version);
				if (versionData.openwrt_version) versionLines.push('OpenWrt: ' + versionData.openwrt_version);
			}
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Version')),
				E('pre', { 'id': 'ov_version', 'class': 'tg-monospace' }, versionLines.join('\n') || '—')
			]));
		}

		function renderWallet() {
			clearNode(tabContent);
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Wallet Balance')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Total Balance')),
					E('div', { 'class': 'cbi-value-field tg-balance', 'id': 'wl_balance' }, 'Loading…')
				]),
				E('pre', { 'id': 'wl_info', 'class': 'tg-monospace tg-scroll-sm' }, 'Loading…')
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Fund Wallet')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title', 'for': 'wl_token' }, _('Cashu Token')),
					E('div', { 'class': 'cbi-value-field' }, [
						E('input', { 'type': 'password', 'id': 'wl_token', 'class': 'cbi-input-password tg-input-max', 'placeholder': _('Paste your Cashu ecash token') })
					]),
					E('div', { 'class': 'cbi-value-description' }, _('Paste a Cashu token to add funds. The token will be consumed.'))
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-apply', 'id': 'wl_fund_btn', 'click': walletFund }, _('Fund Wallet')),
					' ', E('span', { 'id': 'wl_fund_state', 'class': 'tg-hint' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Drain Wallet')),
				E('p', { 'class': 'tg-hint' }, _('Convert all wallet funds to Cashu tokens. Copy the tokens to a safe place.')),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-remove', 'click': walletDrain }, _('Drain All Funds')),
					' ', E('span', { 'id': 'wl_drain_state', 'class': 'tg-hint' })
				]),
				E('pre', { 'id': 'wl_drain_result', 'class': 'tg-hidden tg-monospace tg-scroll-md' }),
				E('div', { 'id': 'wl_drain_copy_wrap', 'class': 'tg-hidden' }, [
					E('button', { 'class': 'cbi-button', 'click': function() {
						var el = q('wl_drain_result');
						var text = el ? el.textContent : '';
						copyToClipboard(text).then(function() {
							var btn = q('wl_drain_copy_btn');
							if (btn) btn.textContent = 'Copied!';
							setTimeout(function() { if (btn) btn.textContent = _('Copy tokens to clipboard'); }, 2000);
						});
					}, 'id': 'wl_drain_copy_btn' }, _('Copy tokens to clipboard'))
				])
			]));

			cliJson('wallet', 'balance').then(function(resp) {
				var el = q('wl_balance');
				if (el && resp && resp.data) el.textContent = (resp.data.balance_sats || 0) + ' sats';
			}).catch(function() {});
			cliJson('wallet', 'info').then(function(resp) {
				var el = q('wl_info');
				if (!el || !resp || !resp.data) return;
				var lines = ['Total: ' + (resp.data.total_balance || 0) + ' sats across ' + (resp.data.mint_count || 0) + ' mint(s)', ''];
				var mints = resp.data.mint_balances || {};
				Object.keys(mints).forEach(function(url) {
					lines.push('  ' + url + ': ' + mints[url] + ' sats');
				});
				el.textContent = lines.join('\n') || 'No info.';
			}).catch(function() {});
		}

		function renderNetwork() {
			clearNode(tabContent);
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Private WiFi Network')),
				E('div', { 'id': 'nw_loading', 'class': 'tg-hint' }, 'Loading…')
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Rename Network')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title', 'for': 'nw_new_ssid' }, _('New SSID')),
					E('div', { 'class': 'cbi-value-field' }, [
						E('input', { 'type': 'text', 'id': 'nw_new_ssid', 'class': 'cbi-input-text tg-input-max', 'placeholder': _('Enter new network name') })
					])
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': wifiRename }, _('Rename')),
					' ', E('span', { 'id': 'nw_rename_state', 'class': 'tg-hint' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Change Password')),
				E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title', 'for': 'nw_new_pw' }, _('New Password')),
					E('div', { 'class': 'cbi-value-field' }, [
						E('input', { 'type': 'password', 'id': 'nw_new_pw', 'class': 'cbi-input-password tg-input-max', 'placeholder': _('Leave empty to generate random') })
					]),
					E('div', { 'class': 'cbi-value-description' }, _('Leave empty to auto-generate a memorable password.'))
				]),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': wifiPassword }, _('Change Password')),
					' ', E('span', { 'id': 'nw_pw_state', 'class': 'tg-hint' })
				])
			]));

			cliJson('network', 'private', 'status').then(function(resp) {
				var d = (resp && resp.data) || {};
				var el = q('nw_loading');
				if (!el) return;
				clearNode(el);
				el.classList.add('tg-loaded');
				var enabled = d.enabled;
				var pwHidden = true;
				el.appendChild(E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Status')),
					E('div', { 'class': 'cbi-value-field' }, enabled
						? badge('Enabled', 'tg-badge-running')
						: badge('Disabled', 'tg-badge-stopped'))
				]));
				el.appendChild(E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('SSID')),
					E('div', { 'class': 'cbi-value-field tg-ssid',  }, d.ssid || '—')
				]));
				var pwSpan = E('span', { 'id': 'nw_pw_display', 'class': 'tg-pw-display' }, '\u2022'.repeat(Math.min((d.password || '').length, 16)));
				el.appendChild(E('div', { 'class': 'cbi-value' }, [
					E('label', { 'class': 'cbi-value-title' }, _('Password')),
					E('div', { 'class': 'cbi-value-field' }, [
						pwSpan, ' ',
						E('button', { 'class': 'cbi-button tg-btn-xs', 'click': function() {
							pwHidden = !pwHidden;
							pwSpan.textContent = pwHidden ? '\u2022'.repeat(Math.min((d.password || '').length, 16)) : (d.password || '—');
							this.textContent = pwHidden ? _('Show') : _('Hide');
						}}, _('Show'))
					])
				]));
				el.appendChild(E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-apply', 'click': function() {
						cliJson('network', 'private', 'enable').then(function() { setTab('network'); });
					}}, _('Enable')),
					E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() {
						ui.showModal(_('Disable Private WiFi'), [
							E('p', _('Disabling the private network may lock you out of the router.')),
							E('div', { 'class': 'right' }, [
								E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')), ' ',
								E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() {
									ui.hideModal();
									cliJson('network', 'private', 'disable').then(function() { setTab('network'); });
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
			clearNode(tabContent);
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Configuration')),
				E('div', { 'id': 'cfg_content', 'class': 'tg-hint' }, 'Loading…')
			]));

			Promise.all([
				cliJson('config', 'get'),
				cachedSchema ? Promise.resolve({ data: cachedSchema }) : cliJson('config', 'schema')
			]).then(function(results) {
				var configResp = results[0];
				var schemaData = (results[1] && results[1].data) || {};
				var cfg = (configResp && configResp.data && configResp.data.config) || {};
				var identities = (configResp && configResp.data && configResp.data.identities) || {};
				var configSchema = schemaData.config || [];
				var identitiesSchema = schemaData.identities || [];

				var container = q('cfg_content');
				if (!container) return;
				clearNode(container);
				container.classList.add('tg-loaded');

				var sections = [];

				configSchema.forEach(function(field) {
					if (field.json_key === 'config_version') return;
					if (field.json_key === 'upstream_detector' || field.json_key === 'upstream_session_manager') return;
					if (!field.editable) return;

					if (field.type === 'array' && field.json_key === 'accepted_mints') {
						sections.push(buildMintsSection(cfg.accepted_mints || [], field));
					} else if (field.type === 'array' && field.json_key === 'profit_share') {
						sections.push(buildProfitShareSection(cfg.profit_share || [], field, identities));
					} else if (field.type === 'string' || field.type === 'uint64' || field.type === 'float64' || field.type === 'bool') {
						sections.push(buildSimpleField(cfg, field));
					}
				});

				sections.push(buildIdentitiesSection(identities, identitiesSchema));
				sections.push(buildAdvancedConfigSection(cfg));

				sections.push(E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': function() { saveAllConfig(false); } }, _('Save')),
					E('button', { 'class': 'cbi-button cbi-button-apply', 'click': function() { saveAllConfig(true); } }, _('Save & Apply')),
					E('button', { 'class': 'cbi-button', 'click': function() { setTab('config'); } }, _('Reset')),
					' ', E('span', { 'id': 'cfg_save_state', 'class': 'tg-hint' })
				]));

				sections.forEach(function(s) { container.appendChild(s); });
			}).catch(function(err) {
				var el = q('cfg_content');
				if (el) el.textContent = 'Failed: ' + err;
			});
		}

		function buildSimpleField(cfg, field) {
			var val = cfg[field.json_key];
			var input;
			if (field.enum) {
				var options = field.enum.map(function(opt) {
					return E('option', { 'value': opt, 'selected': String(val) === opt ? 'selected' : undefined }, opt);
				});
				input = E('select', { 'id': 'cfg_' + field.json_key, 'class': 'cbi-input-select tg-input-sm',  }, options);
			} else if (field.type === 'bool') {
				input = E('select', { 'id': 'cfg_' + field.json_key, 'class': 'cbi-input-select tg-input-sm',  }, [
					E('option', { 'value': 'true', 'selected': val === true ? 'selected' : undefined }, 'true'),
					E('option', { 'value': 'false', 'selected': val === false ? 'selected' : undefined }, 'false')
				]);
			} else {
				input = E('input', {
					'type': 'text', 'id': 'cfg_' + field.json_key, 'class': 'cbi-input-text tg-input-sm',
					'value': val != null ? String(val) : '',
					'placeholder': field.description || ''
				});
			}
			return E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _(field.json_key)),
				E('div', { 'class': 'cbi-value-field' }, [
					input,
					E('div', { 'class': 'cbi-value-description' }, field.description || '')
				])
			]);
		}

		function buildMintCard(mint, idx, field, removable) {
			var numericFields = (field.children || []).filter(function(cf) { return cf.json_key !== 'url'; });
			var urlField = (field.children || []).filter(function(cf) { return cf.json_key === 'url'; })[0];

			var gridCells = numericFields.map(function(cf) {
				return E('div', { 'class': 'tg-mint-grid-item' }, [
					E('label', { 'class': 'tg-field-label' }, cf.json_key.replace(/_/g, ' ')),
					E('input', {
						'type': 'text', 'class': 'cbi-input-text tg-field-sm',
						'id': 'cfg_mint_' + idx + '_' + cf.json_key,
						'value': mint[cf.json_key] != null ? String(mint[cf.json_key]) : (cf.default != null ? String(cf.default) : ''),
						'placeholder': cf.json_key
					})
				]);
			});

			var headerItems = [];
			if (urlField) {
				headerItems.push(E('div', { 'class': 'tg-mint-url' }, [
					E('label', { 'class': 'tg-field-label' }, 'url'),
					E('input', {
						'type': 'text', 'class': 'cbi-input-text tg-field-sm',
						'id': 'cfg_mint_' + idx + '_url',
						'value': mint.url || '',
						'placeholder': 'https://mint.example.com'
					})
				]));
			}
			if (removable) {
				headerItems.push(E('button', {
					'class': 'cbi-button cbi-button-remove tg-mint-remove',
					'click': function() { this.closest('.mint-card').remove(); }
				}, '\u00d7'));
			}

			return E('div', { 'class': 'tg-mint-card' }, [
				E('div', { 'class': 'tg-mint-header' }, headerItems),
				E('div', { 'class': 'tg-mint-grid' }, gridCells)
			]);
		}

		function buildMintsSection(mints, field) {
			var cards = mints.map(function(mint, idx) {
				return buildMintCard(mint, idx, field, true);
			});

			return E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Accepted Mints')),
				E('div', { 'id': 'cfg_mints_body' }, cards),
				E('div', { 'class': 'cbi-page-actions tg-actions-sm' }, [
					E('button', { 'class': 'cbi-button', 'click': function() { addMintRow(field); } }, _('Add Mint'))
				])
			]);
		}

		function addMintRow(field) {
			var container = q('cfg_mints_body');
			if (!container) return;
			var idx = container.children.length;
			container.appendChild(buildMintCard({}, idx, field, true));
		}

		function buildProfitShareSection(shares, field, identities) {
			var identNames = (identities.public_identities || []).map(function(i) { return i.name; });
			var prevState = [];

			function snapshotState() {
				var tbody = q('cfg_ps_body');
				if (!tbody) return;
				prevState = [];
				for (var i = 0; i < tbody.children.length; i++) {
					var el = q('cfg_ps_' + i + '_factor');
					prevState.push(el ? parseFloat(el.value) : 0);
				}
			}

			function writeRow(idx, factor) {
				var rangeEl = q('cfg_ps_' + idx + '_range');
				var labelEl = q('cfg_ps_' + idx + '_pct');
				var hiddenEl = q('cfg_ps_' + idx + '_factor');
				if (rangeEl) rangeEl.value = Math.round(factor * 100);
				if (labelEl) labelEl.textContent = (factor * 100).toFixed(1) + '%';
				if (hiddenEl) hiddenEl.value = factor.toFixed(4);
			}

			function onSliderChange(changedIdx) {
				var tbody = q('cfg_ps_body');
				if (!tbody) return;
				var n = tbody.children.length;
				if (n <= 1) {
					writeRow(changedIdx, 1.0);
					return;
				}

				var changedEl = q('cfg_ps_' + changedIdx + '_range');
				if (!changedEl) return;
				var newVal = parseFloat(changedEl.value) / 100;
				var oldVal = prevState[changedIdx] || 0;
				var delta = newVal - oldVal;
				if (Math.abs(delta) < 0.001) {
					writeRow(changedIdx, newVal);
					return;
				}

				var othersOldSum = 0;
				for (var i = 0; i < n; i++) {
					if (i !== changedIdx) othersOldSum += (prevState[i] || 0);
				}

				var newVals = [];
				newVals[changedIdx] = newVal;

				if (othersOldSum < 0.001) {
					var even = (1.0 - newVal) / (n - 1);
					for (var j = 0; j < n; j++) {
						if (j !== changedIdx) newVals[j] = Math.max(0, even);
					}
				} else {
					for (var j = 0; j < n; j++) {
						if (j === changedIdx) continue;
						var proportion = (prevState[j] || 0) / othersOldSum;
						newVals[j] = Math.max(0, (prevState[j] || 0) - delta * proportion);
					}
				}

				var sum = 0;
				for (var k = 0; k < n; k++) sum += (newVals[k] || 0);
				if (Math.abs(sum - 1.0) > 0.002) {
					var remainder = 1.0 - newVal;
					var othersSum = sum - newVal;
					for (var k = 0; k < n; k++) {
						if (k !== changedIdx && othersSum > 0) {
							newVals[k] = (newVals[k] || 0) * remainder / othersSum;
						}
					}
				}

				for (var k = 0; k < n; k++) {
					if (newVals[k] < 0) newVals[k] = 0;
				}

				for (var k = 0; k < n; k++) {
					writeRow(k, newVals[k]);
				}

				prevState = newVals.slice();
			}

			function removeShareRow(btn) {
				var tr = btn.closest('tr');
				var tbody = tr.parentNode;
				if (!tbody || tbody.children.length <= 1) return;
				tr.remove();
				var n = tbody.children.length;
				var even = n > 0 ? (1.0 / n) : 1.0;
				for (var i = 0; i < n; i++) {
					var row = tbody.children[i];
					var rangeEl = row.querySelector('input[type="range"]');
					var hiddenEl = row.querySelector('input[type="hidden"]');
					var labelEl = row.querySelector('span[id*="_pct"]');
					var selectEl = row.querySelector('select');
					if (rangeEl) { rangeEl.id = 'cfg_ps_' + i + '_range'; rangeEl.value = Math.round(even * 100); rangeEl.dataset.idx = String(i); }
					if (hiddenEl) { hiddenEl.id = 'cfg_ps_' + i + '_factor'; hiddenEl.value = even.toFixed(4); }
					if (labelEl) { labelEl.id = 'cfg_ps_' + i + '_pct'; labelEl.textContent = (even * 100).toFixed(1) + '%'; }
					if (selectEl) { selectEl.id = 'cfg_ps_' + i + '_identity'; }
				}
				snapshotState();
				for (var i = 0; i < n; i++) {
					(function(idx) {
						var rangeEl = q('cfg_ps_' + i + '_range');
						if (rangeEl) {
							rangeEl.removeEventListener('input', rangeEl._sliderFn);
							rangeEl._sliderFn = function() { onSliderChange(idx); };
							rangeEl.addEventListener('input', rangeEl._sliderFn);
						}
					})(i);
				}
			}

			var rows = [];
			shares.forEach(function(share, idx) {
				var pct = share.factor != null ? share.factor : 0;
				var rangeInput;
				rows.push(E('tr', {}, [
					E('td', { 'class': 'tg-slider-td' }, [
						rangeInput = E('input', {
							'type': 'range', 'id': 'cfg_ps_' + idx + '_range',
							'min': '0', 'max': '100', 'step': '1',
							'value': String(Math.round(pct * 100)),
							'class': 'tg-slider',
							'data-idx': String(idx),
						}),
						E('input', {
							'type': 'hidden', 'id': 'cfg_ps_' + idx + '_factor',
							'value': pct.toFixed(4)
						})
					]),
					E('td', { 'class': 'tg-td tg-pct-label' }, [
						E('span', { 'id': 'cfg_ps_' + idx + '_pct' }, (pct * 100).toFixed(1) + '%')
					]),
					E('td', { 'class': 'tg-td' }, [
						E('select', {
							'id': 'cfg_ps_' + idx + '_identity', 'class': 'cbi-input-select tg-field-sm',
							
						}, identNames.map(function(n) {
							return E('option', { 'value': n, 'selected': share.identity === n ? 'selected' : undefined }, n);
						}))
					]),
					E('td', { 'class': 'tg-td' }, [
						E('button', { 'class': 'cbi-button cbi-button-remove tg-btn-remove', 'click': function() { removeShareRow(this); } }, '\u00d7')
					])
				]));
			});

			var section = E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Profit Share')),
				E('p', { 'class': 'tg-hint-sm' }, _('Drag sliders to allocate shares. Total always sums to 100%.')),
				E('div', { 'class': 'tg-overflow-x' }, [
					E('table', { 'class': 'table tg-field-sm',  }, [
						E('thead', {}, [E('tr', {}, [
							E('th', { 'class': 'tg-th' }, 'share'),
							E('th', { 'class': 'tg-th tg-th-pct' }, ''),
							E('th', { 'class': 'tg-th' }, 'identity'),
							E('th', { 'class': 'tg-th tg-th-narrow' }, '')
						])]),
						E('tbody', { 'id': 'cfg_ps_body' }, rows)
					])
				]),
				E('div', { 'class': 'cbi-page-actions tg-actions-sm' }, [
					E('button', { 'class': 'cbi-button', 'click': function() {
						var tbody = q('cfg_ps_body');
						if (!tbody) return;
						var n = tbody.children.length;
						var even = n > 0 ? (1.0 / (n + 1)) : 0.5;
						var idx = n;
						tbody.appendChild(E('tr', {}, [
							E('td', { 'class': 'tg-slider-td' }, [
								E('input', {
									'type': 'range', 'id': 'cfg_ps_' + idx + '_range',
									'min': '0', 'max': '100', 'step': '1', 'value': String(Math.round(even * 100)),
									'class': 'tg-slider',
									'data-idx': String(idx),
								}),
								E('input', {
									'type': 'hidden', 'id': 'cfg_ps_' + idx + '_factor',
									'value': even.toFixed(4)
								})
							]),
							E('td', { 'class': 'tg-td tg-pct-label' }, [
								E('span', { 'id': 'cfg_ps_' + idx + '_pct' }, (even * 100).toFixed(1) + '%')
							]),
							E('td', { 'class': 'tg-td' }, [
								E('select', { 'id': 'cfg_ps_' + idx + '_identity', 'class': 'cbi-input-select tg-field-sm',  },
									identNames.map(function(n) { return E('option', { 'value': n }, n); }))
							]),
							E('td', { 'class': 'tg-td' }, [
								E('button', { 'class': 'cbi-button cbi-button-remove tg-btn-remove', 'click': function() { removeShareRow(this); } }, '\u00d7')
							])
						]));
						snapshotState();
						for (var i = 0; i <= n; i++) {
							(function(idx) {
								var rangeEl = q('cfg_ps_' + i + '_range');
								if (rangeEl) {
									rangeEl.removeEventListener('input', rangeEl._sliderFn);
									rangeEl._sliderFn = function() { onSliderChange(idx); };
									rangeEl.addEventListener('input', rangeEl._sliderFn);
								}
							})(i);
						}
						for (var i = 0; i <= n; i++) onSliderChange(i);
					} }, _('Add Share'))
				])
			]);

			setTimeout(function() {
				var tbody = q('cfg_ps_body');
				if (!tbody) return;
				for (var i = 0; i < tbody.children.length; i++) {
					(function(idx) {
						var rangeEl = q('cfg_ps_' + idx + '_range');
						if (rangeEl && !rangeEl._sliderBound) {
							rangeEl._sliderBound = true;
							rangeEl.addEventListener('input', function() { onSliderChange(idx); });
						}
					})(i);
				}
				snapshotState();
			}, 0);

			return section;
		}

		function buildIdentitiesSection(identities, schema) {
			var rows = [];
			var publicIdents = identities.public_identities || [];
			publicIdents.forEach(function(ident, idx) {
				rows.push(E('tr', {}, [
					E('td', { 'class': 'tg-td' }, [
						E('input', { 'type': 'text', 'class': 'cbi-input-text tg-field-sm', 'id': 'cfg_pi_' + idx + '_name', 'value': ident.name || '' })
					]),
					E('td', { 'class': 'tg-td' }, [
						E('input', { 'type': 'text', 'class': 'cbi-input-text tg-field-sm', 'id': 'cfg_pi_' + idx + '_pubkey', 'value': ident.pubkey || '', 'placeholder': 'hex pubkey' })
					]),
					E('td', { 'class': 'tg-td' }, [
						E('input', { 'type': 'text', 'class': 'cbi-input-text tg-field-sm', 'id': 'cfg_pi_' + idx + '_lightning_address', 'value': ident.lightning_address || '', 'placeholder': 'user@domain' })
					]),
					E('td', { 'class': 'tg-td' }, [
						E('button', { 'class': 'cbi-button cbi-button-remove tg-btn-remove', 'click': function() {
							this.closest('tr').remove();
						} }, '\u00d7')
					])
				]));
			});

			return E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Public Identities')),
				E('p', { 'class': 'tg-hint' }, _('Identities used for profit sharing and trust.')),
				E('div', { 'class': 'tg-overflow-x' }, [
					E('table', { 'class': 'table tg-field-sm',  }, [
						E('thead', {}, [E('tr', {}, [
							E('th', { 'class': 'tg-th' }, 'name'),
							E('th', { 'class': 'tg-th' }, 'pubkey'),
							E('th', { 'class': 'tg-th' }, 'lightning_address'),
							E('th', { 'class': 'tg-th tg-th-narrow' }, '')
						])]),
						E('tbody', { 'id': 'cfg_pi_body' }, rows)
					])
				]),
				E('div', { 'class': 'cbi-page-actions tg-actions-sm' }, [
					E('button', { 'class': 'cbi-button', 'click': function() {
						var tbody = q('cfg_pi_body');
						if (!tbody) return;
						var idx = tbody.children.length;
						tbody.appendChild(E('tr', {}, [
							E('td', { 'class': 'tg-td' }, [E('input', { 'type': 'text', 'class': 'cbi-input-text tg-field-sm', 'id': 'cfg_pi_' + idx + '_name', 'value': '', 'placeholder': 'name' })]),
							E('td', { 'class': 'tg-td' }, [E('input', { 'type': 'text', 'class': 'cbi-input-text tg-field-sm', 'id': 'cfg_pi_' + idx + '_pubkey', 'value': '', 'placeholder': 'hex pubkey' })]),
							E('td', { 'class': 'tg-td' }, [E('input', { 'type': 'text', 'class': 'cbi-input-text tg-field-sm', 'id': 'cfg_pi_' + idx + '_lightning_address', 'value': '', 'placeholder': 'user@domain' })]),
							E('td', { 'class': 'tg-td' }, [
								E('button', { 'class': 'cbi-button cbi-button-remove tg-btn-remove', 'click': function() { this.closest('tr').remove(); } }, '\u00d7')
							])
						]));
					} }, _('Add Identity'))
				])
			]);
		}

		function buildAdvancedConfigSection(cfg) {
			return E('details', { 'class': 'tg-adv-details' }, [
				E('summary', { 'class': 'tg-adv-summary' }, _('Advanced: upstream_detector, upstream_session_manager')),
				E('pre', { 'id': 'cfg_advanced_raw', 'class': 'tg-monospace-sm tg-scroll-lg' },
					JSON.stringify({
						upstream_detector: cfg.upstream_detector || {},
						upstream_session_manager: cfg.upstream_session_manager || {}
					}, null, 2))
			]);
		}

		function coerceByType(val, type) {
			if (val === 'true') return true;
			if (val === 'false') return false;
			if (type === 'uint64' || type === 'int') return parseInt(val, 10);
			if (type === 'float64') return parseFloat(val);
			return val;
		}

		function saveAllConfig(apply) {
			stateSpan('cfg_save_state', 'Saving…', 'tg-state-warning');

			Promise.all([
				cliJson('config', 'get'),
				cachedSchema ? Promise.resolve({ data: cachedSchema }) : cliJson('config', 'schema')
			]).then(function(results) {
				var resp = results[0];
				var schemaData = (results[1] && results[1].data) || {};
				var cfg = (resp && resp.data && resp.data.config) || {};
				var identities = (resp && resp.data && resp.data.identities) || {};
				var configSchema = schemaData.config || [];

				configSchema.forEach(function(field) {
					if (!field.editable) return;
					if (field.json_key === 'accepted_mints' || field.json_key === 'profit_share') return;
					if (field.type === 'object') return;
					if (field.type === 'array' && (field.children || []).length > 0 && field.children[0].type !== 'string') return;

					var el = q('cfg_' + field.json_key);
					if (!el) return;
					cfg[field.json_key] = coerceByType(el.value, field.type);
				});

				var mintSchemaField = configSchema.filter(function(f) { return f.json_key === 'accepted_mints'; })[0];
				var mintChildKeys = (mintSchemaField && mintSchemaField.children || []).map(function(c) { return c.json_key; });

				var tbody = q('cfg_mints_body');
				if (tbody) {
					var mints = [];
					for (var i = 0; i < tbody.children.length; i++) {
						var mint = {};
						mintChildKeys.forEach(function(f) {
							var el = q('cfg_mint_' + i + '_' + f);
							if (!el) return;
							var childField = (mintSchemaField.children || []).filter(function(c) { return c.json_key === f; })[0];
							mint[f] = coerceByType(el.value, childField ? childField.type : 'string');
						});
						if (mint.url) mints.push(mint);
					}
					cfg.accepted_mints = mints;
				}

				var psBody = q('cfg_ps_body');
				if (psBody) {
					var shares = [];
					for (var i = 0; i < psBody.children.length; i++) {
						var factorEl = q('cfg_ps_' + i + '_factor');
						var identEl = q('cfg_ps_' + i + '_identity');
						if (factorEl && identEl) {
							var factorVal = parseFloat(factorEl.value);
							if (isNaN(factorVal) || factorVal < 0 || factorVal > 1.0) {
								throw 'Profit share factor #' + (i+1) + ' is ' + factorEl.value + ' — must be 0.0\u20131.0 (use a ratio like 0.79, not a percentage like 79)';
							}
							shares.push({
								factor: factorVal,
								identity: identEl.value
							});
						}
					}
					var factorSum = shares.reduce(function(s, sh) { return s + sh.factor; }, 0);
					if (shares.length > 0 && Math.abs(factorSum - 1.0) > 0.01) {
						var keepPct = Math.round((1.0 - factorSum) * 100);
						if (keepPct > 0) {
							throw 'Profit share factors sum to ' + factorSum.toFixed(2) + ' (should be 1.0). ' + keepPct + '% of earnings will remain in the wallet each payout cycle. Adjust the factors or confirm this is intended.';
						} else {
							throw 'Profit share factors sum to ' + factorSum.toFixed(2) + ' (should be 1.0). This exceeds 100% — payouts will drain the wallet.';
						}
					}
					cfg.profit_share = shares;
				}

				var piBody = q('cfg_pi_body');
				if (piBody) {
					var pubIdents = [];
					for (var i = 0; i < piBody.children.length; i++) {
						var nameEl = q('cfg_pi_' + i + '_name');
						var pkEl = q('cfg_pi_' + i + '_pubkey');
						var laEl = q('cfg_pi_' + i + '_lightning_address');
						if (nameEl) {
							var ident = { name: nameEl.value };
							if (pkEl && pkEl.value) ident.pubkey = pkEl.value;
							if (laEl && laEl.value) ident.lightning_address = laEl.value;
							pubIdents.push(ident);
						}
					}
					identities.public_identities = pubIdents;
				}

				var configJson = JSON.stringify(cfg);
				var identitiesJson = JSON.stringify(identities);

				return saveJsonViaService('config', configJson).then(function() {
					return saveJsonViaService('identities', identitiesJson);
				});
			}).then(function() {
				if (apply) {
					stateSpan('cfg_save_state', 'Saved. Restarting services…', 'tg-state-success');
					Promise.all([
						fs.exec_direct('/etc/init.d/tollgate-wrt', ['stop']),
						fs.exec_direct('/etc/init.d/nodogsplash', ['stop'])
					]).then(function() {
						return Promise.all([
							fs.exec_direct('/etc/init.d/nodogsplash', ['start']),
							fs.exec_direct('/etc/init.d/tollgate-wrt', ['start'])
						]);
					}).then(function() {
						setTimeout(function() {
							stateSpan('cfg_save_state', 'Saved and applied.', 'tg-state-success');
							setTab('config');
						}, 3000);
					}).catch(function() {
						stateSpan('cfg_save_state', 'Saved but restart failed.', 'tg-state-error');
						setTab('config');
					});
				} else {
					stateSpan('cfg_save_state', 'Saved. Click "Save & Apply" to restart services.', 'tg-state-success');
				}
			}).catch(function(err) {
				stateSpan('cfg_save_state', 'Save failed: ' + err, 'tg-state-error');
			});
		}

		function renderLogs() {
			clearNode(tabContent);
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Service Logs')),
				E('p', { 'class': 'tg-hint' }, _('Recent tollgate-wrt log lines. Auto-refreshes while this tab is open.')),
				E('pre', { 'id': 'logs_box', 'class': 'tg-monospace tg-scroll-box' }, 'Loading…')
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
			clearNode(tabContent);
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', _('Raw JSON Editor')),
				E('p', { 'class': 'tg-hint' }, _('Edit configuration files directly. Changes take effect after saving.')),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button', 'id': 'reload_files_btn', 'click': loadFiles }, _('Reload both files')),
					' ', E('span', { 'id': 'files_state', 'class': 'tg-hint' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', 'config.json'),
				E('textarea', { 'id': 'config_editor', 'class': 'cbi-input-textarea tg-editor', 'spellcheck': 'false' }),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-action', 'click': function() { validateEditor('config_editor', 'config_state'); } }, _('Validate')),
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': function() { saveAdvancedFile('config'); } }, _('Save config.json')),
					' ', E('span', { 'id': 'config_state', 'class': 'tg-state' })
				])
			]));
			tabContent.appendChild(E('div', { 'class': 'cbi-section' }, [
				E('h3', 'identities.json'),
				E('textarea', { 'id': 'identities_editor', 'class': 'cbi-input-textarea tg-editor', 'spellcheck': 'false' }),
				E('div', { 'class': 'cbi-page-actions' }, [
					E('button', { 'class': 'cbi-button cbi-button-action', 'click': function() { validateEditor('identities_editor', 'identities_state'); } }, _('Validate')),
					E('button', { 'class': 'cbi-button cbi-button-save', 'click': function() { saveAdvancedFile('identities'); } }, _('Save identities.json')),
					' ', E('span', { 'id': 'identities_state', 'class': 'tg-state' })
				])
			]));
			loadFiles();
		}

		function loadFiles() {
			stateSpan('files_state', 'Loading…', '');
			cliJson('config', 'get').then(function(resp) {
				var d = (resp && resp.data) || {};
				var cfgEl = q('config_editor');
				if (cfgEl) cfgEl.value = d.config ? JSON.stringify(d.config, null, 2) + '\n' : '// config not found\n';
				var idsEl = q('identities_editor');
				if (idsEl) idsEl.value = d.identities ? JSON.stringify(d.identities, null, 2) + '\n' : '// identities not found\n';
				stateSpan('files_state', 'Loaded ' + new Date().toLocaleTimeString(), '');
			}).catch(function(err) {
				stateSpan('files_state', 'Failed: ' + err, 'tg-state-error');
			});
		}

		function validateEditor(editorId, stateId) {
			var text = (q(editorId) || {}).value || '';
			try { JSON.parse(text); } catch(e) {
				stateSpan(stateId, 'Invalid JSON: ' + e.message, 'tg-state-error');
				return;
			}
			stateSpan(stateId, 'Valid JSON', 'tg-state-success');
		}

		function saveAdvancedFile(type) {
			var editorId = type === 'config' ? 'config_editor' : 'identities_editor';
			var stateId = type === 'config' ? 'config_state' : 'identities_state';
			var text = (q(editorId) || {}).value || '';
			try { JSON.parse(text); } catch(e) {
				stateSpan(stateId, 'Invalid JSON: ' + e.message, 'tg-state-error');
				return;
			}
			stateSpan(stateId, 'Saving…', '');
			saveJsonViaService(type, text).then(function(resp) {
				if (resp && resp.success) {
					stateSpan(stateId, 'Saved. Restart tollgate-wrt to apply.', 'tg-state-success');
				} else {
					stateSpan(stateId, 'Failed: ' + ((resp && resp.error) || 'Unknown error'), 'tg-state-error');
				}
			}).catch(function(err) {
				stateSpan(stateId, 'Error: ' + err, 'tg-state-error');
			});
		}

		function svcControl(action) {
			var statusEl = q('ov_status');
			if (statusEl) { clearNode(statusEl); statusEl.appendChild(badge(action.charAt(0).toUpperCase() + action.slice(1) + 'ing\u2026', 'tg-badge-unknown')); }
			setSvcButtons(true);
			clearPollWarning();

			if (action === 'restart') {
				Promise.all([
					fs.exec_direct('/etc/init.d/tollgate-wrt', ['stop']),
					fs.exec_direct('/etc/init.d/nodogsplash', ['stop'])
				]).then(function() {
					return Promise.all([
						fs.exec_direct('/etc/init.d/nodogsplash', ['start']),
						fs.exec_direct('/etc/init.d/tollgate-wrt', ['start'])
					]);
				}).then(function() {
					setTimeout(function() { setSvcButtons(false); refreshOverview(); }, 3000);
				}).catch(function() { setSvcButtons(false); refreshOverview(); });
			} else {
				Promise.all([
					fs.exec_direct('/etc/init.d/tollgate-wrt', [action]),
					fs.exec_direct('/etc/init.d/nodogsplash', [action])
				]).then(function() {
					setTimeout(function() { setSvcButtons(false); refreshOverview(); }, 3000);
				}).catch(function() { setSvcButtons(false); refreshOverview(); });
			}
		}

		function walletFund() {
			var tokenEl = q('wl_token');
			var btnEl = q('wl_fund_btn');
			var token = (tokenEl || {}).value || '';
			if (!token.trim()) {
				stateSpan('wl_fund_state', 'Enter a token first.', 'tg-state-warning');
				return;
			}
			if (btnEl) btnEl.disabled = true;
			stateSpan('wl_fund_state', 'Funding…', '');
			cliJson('wallet', 'fund', token.trim()).then(function(resp) {
				if (resp && resp.success) {
					stateSpan('wl_fund_state', 'Funded: ' + ((resp.data && resp.data.amount_received) || 0) + ' sats received.', 'tg-state-success');
					if (tokenEl) tokenEl.value = '';
					cliJson('wallet', 'balance').then(function(r) {
						var el = q('wl_balance');
						if (el && r && r.data) el.textContent = (r.data.balance_sats || 0) + ' sats';
					});
				} else {
					stateSpan('wl_fund_state', 'Failed: ' + ((resp && resp.error) || 'Unknown error'), 'tg-state-error');
				}
			}).catch(function(err) {
				stateSpan('wl_fund_state', 'Error: ' + err, 'tg-state-error');
			}).finally(function() {
				if (btnEl) btnEl.disabled = false;
			});
		}

		function walletDrain() {
			ui.showModal(_('Drain Wallet'), [
				E('p', _('This will convert ALL wallet funds to Cashu tokens. Copy them to a safe place.')),
				E('div', { 'class': 'right' }, [
					E('button', { 'class': 'cbi-button', 'click': ui.hideModal }, _('Cancel')), ' ',
					E('button', { 'class': 'cbi-button cbi-button-remove', 'click': function() {
						ui.hideModal();
						stateSpan('wl_drain_state', 'Draining…', '');
						cliJson('wallet', 'drain', 'cashu').then(function(resp) {
							var d = (resp && resp.data) || {};
							var tokens = d.tokens || [];
							var lines = ['Total drained: ' + (d.total_sats || 0) + ' sats', ''];
							tokens.forEach(function(t, i) {
								lines.push('Token ' + (i + 1) + ': ' + (t.balance_sats || 0) + ' sats (' + (t.mint_url || '') + ')');
								if (t.token) lines.push(t.token);
								lines.push('');
							});
							var resultEl = q('wl_drain_result');
							if (resultEl) { resultEl.textContent = lines.join('\n'); resultEl.classList.remove('tg-hidden'); }
							var copyWrap = q('wl_drain_copy_wrap');
							if (copyWrap) { if (tokens.length > 0) copyWrap.classList.remove('tg-hidden'); else copyWrap.classList.add('tg-hidden'); }
							stateSpan('wl_drain_state', 'Drained.', 'tg-state-success');
							cliJson('wallet', 'balance').then(function(r) {
								var el = q('wl_balance');
								if (el && r && r.data) el.textContent = (r.data.balance_sats || 0) + ' sats';
							});
						}).catch(function(err) {
							stateSpan('wl_drain_state', 'Error: ' + err, 'tg-state-error');
						});
					}}, _('Confirm'))
				])
			]);
		}

		function wifiRename() {
			var ssidEl = q('nw_new_ssid');
			var ssid = (ssidEl || {}).value || '';
			if (!ssid.trim()) { stateSpan('nw_rename_state', 'Enter a new SSID.', 'tg-state-warning'); return; }
			stateSpan('nw_rename_state', 'Renaming…', '');
			cliJson('network', 'private', 'rename', ssid.trim()).then(function(resp) {
				if (resp && resp.success) {
					stateSpan('nw_rename_state', 'Renamed to ' + ssid, 'tg-state-success');
					if (ssidEl) ssidEl.value = '';
					setTab('network');
				} else {
					stateSpan('nw_rename_state', 'Failed: ' + ((resp && resp.error) || 'Unknown error'), 'tg-state-error');
				}
			}).catch(function(err) {
				stateSpan('nw_rename_state', 'Error: ' + err, 'tg-state-error');
			});
		}

		function wifiPassword() {
			var pwEl = q('nw_new_pw');
			var pw = (pwEl || {}).value || '';
			stateSpan('nw_pw_state', 'Changing…', '');
			var args = pw.trim() ? ['network', 'private', 'set-password', pw.trim()] : ['network', 'private', 'set-password'];
			cliJson.apply(null, args).then(function(resp) {
				if (resp && resp.success) {
					var newPw = (resp.data && resp.data.new_password) || '';
					stateSpan('nw_pw_state', newPw ? 'New password: ' + newPw : 'Password changed.', 'tg-state-success');
					if (pwEl) pwEl.value = '';
					setTimeout(function() { setTab('network'); }, 3000);
				} else {
					stateSpan('nw_pw_state', 'Failed: ' + ((resp && resp.error) || 'Unknown error'), 'tg-state-error');
				}
			}).catch(function(err) {
				stateSpan('nw_pw_state', 'Error: ' + err, 'tg-state-error');
			});
		}

		function refreshOverview() {
			cliJson('wallet', 'balance').then(function(resp) {
				pollFailCount = 0;
				clearPollWarning();
				var text = (resp && resp.data) ? (resp.data.balance_sats || 0) + ' sats' : '—';
				var el = q('ov_balance');
				if (el) el.textContent = text;
				var wlEl = q('wl_balance');
				if (wlEl) wlEl.textContent = text;
			}).catch(function() { pollFailCount++; showPollWarning(); });
			cliJson('status').then(function(resp) {
				var el = q('ov_status');
				if (el) {
					clearNode(el);
					var running = resp && resp.data && resp.data.running;
					el.appendChild(statusBadge(running));
				}
			}).catch(function() {});
		}

		function showPollWarning() {
			if (pollFailCount < 3) return;
			var existing = q('poll_warning');
			if (existing) return;
			var header = tabContent.parentNode;
			if (!header) return;
			var warn = E('div', { 'id': 'poll_warning', 'class': 'alert-message warning tg-poll-warning' }, _('Connection to service lost. Retrying…'));
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

		var cssLink = document.createElement('link');
		cssLink.rel = 'stylesheet';
		cssLink.href = L.resource('tollgate-payments/tg.css');
		document.head.appendChild(cssLink);

		var viewEl = E('div', { 'class': 'cbi-map' }, [
			E('h2', { 'name': 'content' }, 'TollGate'),
			E('div', { 'class': 'cbi-map-descr' }, _('Manage your TollGate captive portal payment gateway.')),
			E('div', { 'class': 'tg-tab-bar' }, [
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
