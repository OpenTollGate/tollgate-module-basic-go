import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const username = process.env.TOLLGATE_LUCI_USER;
const password = process.env.TOLLGATE_LUCI_PASSWORD;
if (!username || !password) {
	console.error('Error: TOLLGATE_LUCI_USER and TOLLGATE_LUCI_PASSWORD env vars are required');
	process.exit(1);
}
const url = process.env.TOLLGATE_LUCI_URL ?? 'http://192.168.13.112:8080/cgi-bin/luci/admin/services/tollgate-payments';

async function loginIfNeeded(page) {
	if (!(await page.getByText('Authorization Required').count())) return;
	await page.getByRole('textbox', { name: 'Username' }).fill(username);
	await page.getByRole('textbox', { name: 'Password' }).fill(password);
	await page.getByRole('button', { name: 'Log in' }).click();
	await page.getByRole('heading', { name: 'TollGate' }).waitFor();
}

function qValue(id) {
	const el = document.getElementById(id);
	return el ? el.value : '';
}

async function waitForConfigLoaded(page) {
	await page.waitForFunction(() => {
		const el = document.getElementById('cfg_content');
		return el && el.textContent !== 'Loading…' && el.style.opacity === '1';
	}, { timeout: 15000 });
}

async function waitForSaveState(page, match, timeout = 10000) {
	if (match) {
		await page.waitForFunction(
			([m]) => {
				const el = document.getElementById('cfg_save_state');
				return el && el.textContent.includes(m);
			},
			[match],
			{ timeout }
		);
	} else {
		await page.waitForTimeout(1000);
	}
	return await page.evaluate(() => {
		const el = document.getElementById('cfg_save_state');
		return el ? el.textContent.trim() : '';
	});
}

async function getProfitShareRows(page) {
	return await page.evaluate(() => {
		const tbody = document.getElementById('cfg_ps_body');
		if (!tbody) return [];
		const rows = [];
		for (let i = 0; i < tbody.children.length; i++) {
			const factorEl = document.getElementById('cfg_ps_' + i + '_factor');
			const identEl = document.getElementById('cfg_ps_' + i + '_identity');
			if (factorEl && identEl) {
				rows.push({ factor: factorEl.value, identity: identEl.value, index: i });
			}
		}
		return rows;
	});
}

async function getIdentityRows(page) {
	return await page.evaluate(() => {
		const tbody = document.getElementById('cfg_pi_body');
		if (!tbody) return [];
		const rows = [];
		for (let i = 0; i < tbody.children.length; i++) {
			const nameEl = document.getElementById('cfg_pi_' + i + '_name');
			if (nameEl) {
				const pubkeyEl = document.getElementById('cfg_pi_' + i + '_pubkey');
				const laEl = document.getElementById('cfg_pi_' + i + '_lightning_address');
				rows.push({
					name: nameEl.value,
					pubkey: pubkeyEl ? pubkeyEl.value : '',
					lightning_address: laEl ? laEl.value : '',
					index: i,
					columns: nameEl.closest('tr') ? nameEl.closest('tr').children.length : 0
				});
			}
		}
		return rows;
	});
}

async function saveConfig(page) {
	await page.evaluate(() => {
		const btn = document.querySelector('#cfg_content .cbi-button-save');
		if (btn) btn.click();
	});
}

async function run() {
	const browser = await chromium.launch({ headless: true });
	const page = await browser.newPage();
	let passed = 0;
	let failed = 0;

	function check(name, ok) {
		if (ok) {
			console.log(`PASS: ${name}`);
			passed++;
		} else {
			console.log(`FAIL: ${name}`);
			failed++;
		}
	}

	try {
		await page.goto(url, { waitUntil: 'networkidle' });
		await loginIfNeeded(page);

		// Navigate to Configuration tab
		await page.getByRole('button', { name: 'Configuration' }).click();
		await waitForConfigLoaded(page);

		// --- Save original profit_share and identities state ---
		const originalShares = await getProfitShareRows(page);
		const originalIdentities = await getIdentityRows(page);
		const ps0 = originalShares[0];
		console.log(`  Original profit_share: ${originalShares.map(s => s.factor + ':' + s.identity).join(', ')}`);
		console.log(`  Original identities: ${originalIdentities.map(i => i.name).join(', ')}`);

		// === TEST 1: Factor >1.0 rejected (server catches bypassed hidden input) ===
		console.log('\n--- Test 1: Factor >1.0 rejected via server ---');
		if (ps0) {
			await page.evaluate(({ idx }) => {
				var hidden = document.getElementById('cfg_ps_' + idx + '_factor');
				if (hidden) hidden.value = '79';
			}, { idx: ps0.index });

			await saveConfig(page);
			await page.waitForTimeout(2000);
			const state1 = await page.evaluate(() => {
				const el = document.getElementById('cfg_save_state');
				return el ? el.textContent.trim() : '';
			});
			console.log(`  Save state: "${state1}"`);
			check('factor >1.0 rejected (server catches bypassed slider)', state1.includes('Failed') || state1.includes('Invalid') || state1.includes('failed'));

			await page.evaluate(({ idx, origVal }) => {
				var hidden = document.getElementById('cfg_ps_' + idx + '_factor');
				if (hidden) hidden.value = origVal;
			}, { idx: ps0.index, origVal: ps0.factor });
		} else {
			check('factor >1.0 test (no rows)', false);
		}

		// === TEST 2: Factor <0 rejected (bypass slider) ===
		console.log('\n--- Test 2: Negative factor rejected ---');
		if (ps0) {
			await page.evaluate(({ idx }) => {
				var hidden = document.getElementById('cfg_ps_' + idx + '_factor');
				if (hidden) hidden.value = '-0.5';
			}, { idx: ps0.index });

			await saveConfig(page);
			await page.waitForTimeout(2000);
			const state2 = await page.evaluate(() => {
				const el = document.getElementById('cfg_save_state');
				return el ? el.textContent.trim() : '';
			});
			console.log(`  Save state: "${state2}"`);
			check('negative factor rejected', state2.includes('Failed') || state2.includes('Invalid') || state2.includes('failed'));

			await page.evaluate(({ idx, origVal }) => {
				var hidden = document.getElementById('cfg_ps_' + idx + '_factor');
				if (hidden) hidden.value = origVal;
			}, { idx: ps0.index, origVal: ps0.factor });
		} else {
			check('negative factor test (no rows)', false);
		}

		// === TEST 3: Sliders are present and auto-balance ===
		console.log('\n--- Test 3: Sliders present and interactive ---');
		const hasSliders = await page.evaluate(() => {
			var tbody = document.getElementById('cfg_ps_body');
			if (!tbody) return false;
			var range = tbody.querySelector('input[type="range"]');
			return !!range;
		});
		check('profit share has range sliders', hasSliders);

		if (hasSliders && originalShares.length >= 2) {
			const afterDrag = await page.evaluate(() => {
				var range0 = document.getElementById('cfg_ps_0_range');
				if (!range0) return null;
				range0.value = '50';
				if (range0._handler) range0._handler();
				var vals = [];
				var tbody = document.getElementById('cfg_ps_body');
				for (var i = 0; i < tbody.children.length; i++) {
					var pct = document.getElementById('cfg_ps_' + i + '_pct');
					var factor = document.getElementById('cfg_ps_' + i + '_factor');
					vals.push({ pct: pct ? pct.textContent : '', factor: factor ? factor.value : '' });
				}
				return vals;
			});
			console.log(`  After drag to 50%: ${afterDrag.map(v => v.pct).join(', ')}`);
			const sum = afterDrag.reduce((s, v) => s + parseFloat(v.factor || 0), 0);
			check('sliders auto-balance to sum=1.0', Math.abs(sum - 1.0) < 0.02);
		}

		// === TEST 4: Valid factors (sliders maintain sum=1.0) save OK ===
		console.log('\n--- Test 4: Valid factors save OK ---');
		await saveConfig(page);
		const state4 = await waitForSaveState(page, 'Saved');
		console.log(`  Save state: "${state4}"`);
		check('valid factor saves', state4.includes('Saved'));

		// === TEST 5: Restore original profit_share ===
		console.log('\n--- Test 5: Restore original profit_share ---');
		await page.getByRole('button', { name: 'Overview' }).click();
		await page.waitForTimeout(500);
		await page.getByRole('button', { name: 'Configuration' }).click();
		await waitForConfigLoaded(page);

		const restoredShares = await getProfitShareRows(page);
		console.log(`  Restored: ${restoredShares.map(s => s.factor + ':' + s.identity).join(', ')}`);
		check('profit_share restored after reload', restoredShares.length >= 1);

		// Restore original values if needed
		let needsRestore = false;
		for (let i = 0; i < originalShares.length; i++) {
			const current = restoredShares[i];
			if (!current || current.factor !== originalShares[i].factor || current.identity !== originalShares[i].identity) {
				needsRestore = true;
				break;
			}
		}
		if (needsRestore || restoredShares.length !== originalShares.length) {
			console.log('  Restoring original profit_share values...');
			await page.evaluate((shares) => {
				var tbody = document.getElementById('cfg_ps_body');
				if (!tbody) return;
				while (tbody.firstChild) tbody.removeChild(tbody.firstChild);
				shares.forEach(function(s, i) {
					var tr = document.createElement('tr');
					tr.innerHTML =
						'<td style="padding:4px 6px;width:60%">' +
						'<input type="range" id="cfg_ps_' + i + '_range" min="0" max="100" step="1" value="' + Math.round(s.factor * 100) + '" style="width:100%;vertical-align:middle" data-idx="' + i + '">' +
						'<input type="hidden" id="cfg_ps_' + i + '_factor" value="' + s.factor + '">' +
						'</td>' +
						'<td style="padding:2px 6px;white-space:nowrap;font-weight:600;font-size:13px;min-width:60px;text-align:center">' +
						'<span id="cfg_ps_' + i + '_pct">' + (s.factor * 100).toFixed(1) + '%</span>' +
						'</td>' +
						'<td style="padding:2px 6px">' +
						'<input type="text" class="cbi-input-text" id="cfg_ps_' + i + '_identity" value="' + s.identity + '" style="width:100%;font-size:12px">' +
						'</td>' +
						'<td style="padding:2px 6px">' +
						'<button class="cbi-button cbi-button-remove" style="font-size:11px;padding:1px 6px" onclick="this.closest(\'tr\').remove()">\u00d7</button>' +
						'</td>';
					tbody.appendChild(tr);
				});
			}, originalShares.map(function(s) { return { factor: parseFloat(s.factor), identity: s.identity }; }));
			await saveConfig(page);
			await waitForSaveState(page, 'Saved', 10000);
			console.log('  Original profit_share restored.');
		}

		// === TEST 6: Identity remove button exists and works ===
		console.log('\n--- Test 6: Identity remove button ---');
		await page.getByRole('button', { name: 'Overview' }).click();
		await page.waitForTimeout(500);
		await page.getByRole('button', { name: 'Configuration' }).click();
		await waitForConfigLoaded(page);

		const identRows = await getIdentityRows(page);
		console.log(`  Identity rows: ${identRows.length}, columns per row: ${identRows[0] ? identRows[0].columns : 'N/A'}`);
		check('identity rows have remove column (4 columns = name+pubkey+lightning+remove)', identRows.length > 0 && identRows[0].columns === 4);

		// Actually click a remove button on a non-critical identity (skip first if only 1)
		if (identRows.length > 1) {
			const beforeCount = identRows.length;
			await page.evaluate(() => {
				const tbody = document.getElementById('cfg_pi_body');
				if (!tbody || !tbody.lastElementChild) return;
				const removeBtn = tbody.lastElementChild.querySelector('.cbi-button-remove');
				if (removeBtn) removeBtn.click();
			});
			const afterRows = await getIdentityRows(page);
			console.log(`  Before: ${beforeCount} rows, After remove: ${afterRows.length} rows`);
			check('identity remove button removes row', afterRows.length === beforeCount - 1);

			// Save and verify persistence
			await saveConfig(page);
			await waitForSaveState(page, 'Saved', 10000);

			await page.getByRole('button', { name: 'Overview' }).click();
			await page.waitForTimeout(500);
			await page.getByRole('button', { name: 'Configuration' }).click();
			await waitForConfigLoaded(page);

			const persistedRows = await getIdentityRows(page);
			check('identity removal persists after save+reload', persistedRows.length === beforeCount - 1);

			// Restore original identities by re-adding
			console.log('  Restoring original identities...');
			await page.evaluate((originalIdents) => {
				const tbody = document.getElementById('cfg_pi_body');
				if (!tbody) return;
				while (tbody.firstChild) tbody.removeChild(tbody.firstChild);
				originalIdents.forEach((ident, i) => {
					const tr = document.createElement('tr');
					tr.innerHTML = `
						<td style="padding:2px 6px"><input type="text" class="cbi-input-text" id="cfg_pi_${i}_name" value="${ident.name || ''}" style="width:100%;font-size:12px"></td>
						<td style="padding:2px 6px"><input type="text" class="cbi-input-text" id="cfg_pi_${i}_pubkey" value="${ident.pubkey || ''}" style="width:100%;font-size:12px" placeholder="hex pubkey"></td>
						<td style="padding:2px 6px"><input type="text" class="cbi-input-text" id="cfg_pi_${i}_lightning_address" value="${ident.lightning_address || ''}" style="width:100%;font-size:12px" placeholder="user@domain"></td>
						<td style="padding:2px 6px"><button class="cbi-button cbi-button-remove" style="font-size:11px;padding:1px 6px" onclick="this.closest('tr').remove()">×</button></td>
					`;
					tbody.appendChild(tr);
				});
			}, originalIdentities);
			await saveConfig(page);
			await waitForSaveState(page, 'Saved', 10000);
			console.log('  Original identities restored.');
		}

		// === TEST 7: Server-side validation for factor >1.0 (via Advanced raw JSON) ===
		console.log('\n--- Test 7: Server rejects factor >1.0 via raw JSON ---');
		await page.getByRole('button', { name: 'Advanced' }).click();
		await page.waitForFunction(() => {
			const cfg = document.getElementById('config_editor');
			return cfg && cfg.value && cfg.value.length > 10;
		}, { timeout: 10000 });

		const rawConfig = JSON.parse(await page.locator('#config_editor').inputValue());
		const originalRawShares = rawConfig.profit_share;

		rawConfig.profit_share = [{ factor: 100, identity: 'owner' }];
		await page.locator('#config_editor').fill(JSON.stringify(rawConfig, null, 2));
		await page.getByRole('button', { name: 'Save config.json' }).click();

		await page.waitForFunction(() => {
			const el = document.getElementById('config_state');
			return el && el.textContent.length > 5 && el.textContent !== 'Saving…';
		}, { timeout: 8000 });
		const serverState = await page.evaluate(() => {
			const el = document.getElementById('config_state');
			return el ? el.textContent.trim() : '';
		});
		console.log(`  Server response: "${serverState}"`);
		check('server rejects factor >1.0 via raw JSON', serverState.includes('Failed') || serverState.includes('Invalid') || serverState.includes('> 1.0'));

		// Restore config
		rawConfig.profit_share = originalRawShares;
		await page.locator('#config_editor').fill(JSON.stringify(rawConfig, null, 2));
		await page.getByRole('button', { name: 'Save config.json' }).click();
		await page.waitForFunction(() => {
			const el = document.getElementById('config_state');
			return el && el.textContent.includes('Saved');
		}, { timeout: 8000 });
		console.log('  Config restored after server-side test.');

		// === Summary ===
		console.log(`\n=== Results: ${passed} passed, ${failed} failed ===`);
		if (failed > 0) {
			process.exit(1);
		}
	} finally {
		await browser.close();
	}
}

run().catch((error) => {
	console.error(error);
	process.exit(1);
});
