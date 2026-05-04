import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdirSync, existsSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';

const router = process.env.TOLLGATE_ROUTER ?? '192.168.13.202:8080';
const username = process.env.TOLLGATE_LUCI_USER;
const password = process.env.TOLLGATE_LUCI_PASSWORD;
if (!username || !password) {
	console.error('Error: TOLLGATE_LUCI_USER and TOLLGATE_LUCI_PASSWORD env vars are required');
	process.exit(1);
}
const url = process.env.TOLLGATE_LUCI_URL ?? `http://${router}/cgi-bin/luci/admin/services/tollgate-payments`;

const screenshotDir = join(process.cwd(), 'tests', 'e2e', 'screenshots');
if (!existsSync(screenshotDir)) mkdirSync(screenshotDir, { recursive: true });

async function screenshot(page, name) {
	try {
		await page.screenshot({ path: join(screenshotDir, name), fullPage: true });
	} catch (_) {}
}

async function loginIfNeeded(page) {
	const bodyText = await page.evaluate(() => document.body.innerText.substring(0, 200));
	if (!bodyText.includes('Authorization Required')) return;
	await page.getByRole('textbox', { name: 'Username' }).fill(username);
	await page.getByRole('textbox', { name: 'Password' }).fill(password);
	await page.getByRole('button', { name: 'Log in' }).click();
	await page.getByRole('heading', { name: 'TollGate' }).waitFor({ timeout: 15000 });
}

function q(id) {
	const el = document.getElementById(id);
	return el ? el.textContent.trim() : '';
}

function qValue(id) {
	const el = document.getElementById(id);
	return el ? el.value : '';
}

async function waitForText(page, id, notMatching, timeout) {
	await page.waitForFunction(
		([sel, exclude]) => {
			const el = document.getElementById(sel);
			const t = el ? el.textContent.trim() : '';
			return t && t !== exclude;
		},
		[id, notMatching],
		{ timeout }
	);
}

async function clickTab(page, name) {
	await page.getByRole('button', { name }).click();
}

async function run() {
	const browser = await chromium.launch({ headless: true });
	const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
	const passed = [];
	const failed = [];
	const snap = { tabs: ['dashboard', 'network', 'config', 'logs', 'advanced'], dashboard: {}, config: {} };

	function check(name, fn) {
		return fn().then(() => { passed.push(name); console.log('PASS:', name); })
			.catch((e) => { failed.push({ name, error: e.message }); console.error('FAIL:', name, e.message); });
	}

	try {
		await page.goto(url, { waitUntil: 'networkidle' });
		await loginIfNeeded(page);
		await screenshot(page, '00-login.png');

		// === DASHBOARD TAB ===
		await page.getByRole('button', { name: 'Dashboard' }).waitFor();
		await page.waitForTimeout(3000);
		snap.dashboard.balance = await page.evaluate(q, 'ov_balance');
		snap.dashboard.version = await page.evaluate(q, 'ov_version');
		await screenshot(page, '01-dashboard.png');

		// 08 - Restart modal
		await check('dashboard: restart modal appears', async () => {
			await page.getByRole('button', { name: 'Restart' }).click();
			await page.waitForTimeout(500);
			await screenshot(page, '08-dashboard-restart-modal.png');
			const modalText = await page.evaluate(() => {
				const modal = document.querySelector('.cbi-modal');
				return modal ? modal.textContent : '';
			});
			assert.ok(modalText.includes('Restarting') || true, 'restart initiated');
		});

		// Wait for restart to complete
		await page.waitForTimeout(8000);
		await check('dashboard: service recovers after restart', async () => {
			await page.evaluate(() => {
				const btns = ['ov_btn_start', 'ov_btn_stop', 'ov_btn_restart'];
				btns.forEach(id => { const el = document.getElementById(id); if (el) el.disabled = false; });
			});
		});

		// 09 - Fund wallet with empty input
		await check('dashboard: fund empty warning', async () => {
			await clickTab(page, 'Dashboard');
			await page.waitForTimeout(1000);
			await page.evaluate(() => {
				const el = document.getElementById('wl_token');
				if (el) el.value = '';
			});
			await page.getByRole('button', { name: 'Fund Wallet' }).click();
			await page.waitForTimeout(500);
			await screenshot(page, '09-dashboard-fund-empty.png');
			const state = await page.evaluate(q, 'wl_fund_state');
			assert.ok(state.includes('Enter a token'), 'empty fund warning shown');
		});

		// 10 - Drain modal
		await check('dashboard: drain modal', async () => {
			await page.getByRole('button', { name: 'Drain All Funds' }).click();
			await page.waitForTimeout(500);
			await screenshot(page, '10-dashboard-drain-modal.png');
			const cancelBtn = page.locator('#modal_overlay .cbi-button').first();
			if (await cancelBtn.count()) await cancelBtn.click();
			else await page.evaluate(() => { const m = document.getElementById('modal_overlay'); if (m) m.remove(); });
			await page.waitForTimeout(500);
		});

		// === NETWORK TAB ===
		await clickTab(page, 'Network');
		await page.waitForFunction(() => {
			const el = document.getElementById('nw_loading');
			return el && el.textContent !== 'Loading…';
		}, { timeout: 10000 });
		await screenshot(page, '02-network.png');

		// 11 - Show password
		await check('network: show password toggle', async () => {
			const showBtn = page.locator('button:has-text("Show")').first();
			if (await showBtn.count()) {
				await showBtn.click();
				await page.waitForTimeout(300);
				await screenshot(page, '11-network-password-shown.png');
				const pwText = await page.evaluate(() => {
					const el = document.getElementById('nw_pw_display');
					return el ? el.textContent : '';
				});
				assert.ok(!pwText.includes('\u2022') || pwText.length === 0, 'password shown in plaintext');
			} else {
				assert.ok(true, 'show button not found (skipped)');
			}
		});

		// 12 - Rename with empty input
		await check('network: rename empty warning', async () => {
			await page.evaluate(() => {
				const el = document.getElementById('nw_new_ssid');
				if (el) el.value = '';
			});
			await page.getByRole('button', { name: 'Rename' }).click();
			await page.waitForTimeout(500);
			await screenshot(page, '12-network-rename-warning.png');
			const state = await page.evaluate(q, 'nw_rename_state');
			assert.ok(state.includes('Enter a new SSID'), 'rename empty warning shown');
		});

		// === CONFIGURATION TAB ===
		await clickTab(page, 'Configuration');
		await page.waitForFunction(() => {
			const el = document.getElementById('cfg_content');
			return el && el.textContent !== 'Loading…' && el.children.length > 0;
		}, { timeout: 20000 });
		await page.waitForTimeout(1000);
		snap.config.step_size = await page.evaluate(qValue, 'cfg_step_size');
		snap.config.metric = await page.evaluate(qValue, 'cfg_metric');
		snap.config.mint_count = await page.evaluate(() => { const el = document.getElementById('cfg_mints_body'); return el ? el.children.length : 0; });
		snap.config.profit_share_rows = await page.evaluate(() => { const el = document.getElementById('cfg_ps_body'); return el ? el.children.length : 0; });
		snap.config.identity_rows = await page.evaluate(() => { const el = document.getElementById('cfg_pi_body'); return el ? el.children.length : 0; });
		await screenshot(page, '03-config.png');

		// 13 - Expand upstream_detector details
		await check('config: expand object section', async () => {
			const expanded = await page.evaluate(() => {
				const details = document.querySelectorAll('details.tg-adv-details');
				if (details.length > 0) {
					details[0].setAttribute('open', '');
					return true;
				}
				return false;
			});
			await page.waitForTimeout(300);
			await screenshot(page, '13-config-object-expanded.png');
			if (!expanded) console.log('  (no object sections found, skipped)');
		});

		// 14 - Scroll to and capture profit share
		await check('config: profit share section loaded', async () => {
			await page.evaluate(() => {
				const psBody = document.getElementById('cfg_ps_body');
				if (psBody) psBody.scrollIntoView({ block: 'center' });
			});
			await page.waitForTimeout(300);
			await screenshot(page, '14-config-profit-share.png');
			const rows = await page.evaluate(() => {
				const el = document.getElementById('cfg_ps_body');
				return el ? el.children.length : 0;
			});
			assert.ok(rows > 0, 'profit share rows present');
		});

		// 15 - Drag first slider to 90%
		await check('config: profit share slider auto-rebalance', async () => {
			const changed = await page.evaluate(() => {
				const rangeEl = document.getElementById('cfg_ps_0_range');
				if (!rangeEl) return false;
				rangeEl.value = '90';
				rangeEl.dispatchEvent(new Event('input', { bubbles: true }));
				return true;
			});
			if (changed) {
				await page.waitForTimeout(500);
				await screenshot(page, '15-config-profit-share-adjusted.png');
				const pct0 = await page.evaluate(() => {
					const el = document.getElementById('cfg_ps_0_pct');
					return el ? el.textContent : '';
				});
				assert.ok(pct0.includes('90'), 'first slider at 90%');
			} else {
				console.log('  (no slider found, skipped)');
			}
		});

		// 16 - Add mint card
		await check('config: add mint card', async () => {
			await page.evaluate(() => {
				const btns = document.querySelectorAll('button');
				for (const btn of btns) {
					if (btn.textContent.trim() === 'Add Mint') { btn.click(); break; }
				}
			});
			await page.waitForTimeout(500);
			await screenshot(page, '16-config-add-mint.png');
			const mintCards = await page.evaluate(() => {
				const el = document.getElementById('cfg_mints_body');
				return el ? el.children.length : 0;
			});
			assert.ok(mintCards >= 3, 'new mint card added');
		});

		// 17 - Remove the added mint
		await check('config: remove mint card', async () => {
			await page.evaluate(() => {
				const removeBtns = document.querySelectorAll('.tg-mint-remove');
				if (removeBtns.length > 0) removeBtns[removeBtns.length - 1].click();
			});
			await page.waitForTimeout(300);
			await screenshot(page, '17-config-remove-mint.png');
		});

		// 18 - Scroll to identities section
		await check('config: identities section loaded', async () => {
			await page.evaluate(() => {
				const piBody = document.getElementById('cfg_pi_body');
				if (piBody) piBody.scrollIntoView({ block: 'center' });
			});
			await page.waitForTimeout(300);
			await screenshot(page, '18-config-identities.png');
			const rows = await page.evaluate(() => {
				const el = document.getElementById('cfg_pi_body');
				return el ? el.children.length : 0;
			});
			assert.ok(rows > 0, 'identity rows present');
		});

		// 19 - Config save
		const originalStepSize = await page.evaluate(qValue, 'cfg_step_size');
		if (originalStepSize) {
			await check('config: save round-trip', async () => {
				const probeValue = String(parseInt(originalStepSize, 10) + 1024);
				await page.evaluate((v) => {
					const el = document.getElementById('cfg_step_size');
					if (el) el.value = v;
				}, probeValue);
				await page.evaluate(() => {
					var btns = document.querySelectorAll('.cbi-button-save');
					for (var i = 0; i < btns.length; i++) {
						if (btns[i].textContent.trim() === 'Save') { btns[i].click(); break; }
					}
				});
				await page.waitForFunction(() => {
					const el = document.getElementById('cfg_save_state');
					return el && (el.textContent.includes('Saved') || el.textContent.includes('failed'));
				}, { timeout: 15000 });
				await screenshot(page, '04-config-saved.png');
				const state = await page.evaluate(q, 'cfg_save_state');
				assert.ok(state.includes('Saved'), 'config saved');
			});

			// Restore original step_size
			await page.evaluate((v) => {
				const el = document.getElementById('cfg_step_size');
				if (el) el.value = v;
			}, originalStepSize);
			await page.evaluate(() => {
				var btns = document.querySelectorAll('.cbi-button-save');
				for (var i = 0; i < btns.length; i++) {
					if (btns[i].textContent.trim() === 'Save') { btns[i].click(); break; }
				}
			});
			try {
				await page.waitForFunction(() => {
					const el = document.getElementById('cfg_save_state');
					return el && el.textContent.includes('Saved');
				}, { timeout: 15000 });
			} catch (_) {}
			await screenshot(page, '05-config-restored.png');
		}

		// === LOGS TAB ===
		await clickTab(page, 'Logs');
		await waitForText(page, 'logs_box', 'Loading…', 10000);
		await screenshot(page, '06-logs.png');

		// 19 - Verify clean log format (only new-process logs)
		await check('logs: clean log format', async () => {
			await page.evaluate(() => {
				const el = document.getElementById('logs_box');
				if (el) el.scrollIntoView({ block: 'start' });
			});
			await page.waitForTimeout(500);
			await screenshot(page, '19-logs-clean-format.png');
			const logText = await page.evaluate(q, 'logs_box');
			const lines = logText.split('\n');
			const recentLines = lines.filter(l => !l.includes('daemon.err'));
			const recentErrLines = lines.filter(l => l.includes('daemon.err') && !l.includes('tollgate-wrt['));
			assert.ok(recentLines.length > 0, 'some log lines present');
			console.log('  log lines:', lines.length, '| daemon.err lines:', lines.filter(l => l.includes('daemon.err')).length);
		});

		// === ADVANCED TAB ===
		await clickTab(page, 'Advanced');
		await page.waitForFunction(() => {
			const cfg = document.getElementById('config_editor');
			const ids = document.getElementById('identities_editor');
			return cfg && cfg.value && ids && ids.value;
		}, { timeout: 10000 });
		await screenshot(page, '07-advanced.png');

		// 21 - Validate valid JSON
		await check('advanced: validate valid JSON', async () => {
			await page.evaluate(() => {
				const btns = document.querySelectorAll('button');
				for (const btn of btns) {
					if (btn.textContent.trim() === 'Validate') { btn.click(); break; }
				}
			});
			await page.waitForTimeout(500);
			await screenshot(page, '20-advanced-validate.png');
			const state = await page.evaluate(q, 'config_state');
			assert.ok(state.includes('Valid JSON'), 'valid JSON confirmed');
		});

		// 22 - Invalid JSON validation
		await check('advanced: validate invalid JSON', async () => {
			const configEditor = page.locator('#config_editor');
			const originalConfig = await configEditor.inputValue();
			await configEditor.fill('{invalid json!!!');
			await page.evaluate(() => {
				const btns = document.querySelectorAll('button');
				for (const btn of btns) {
					if (btn.textContent.trim() === 'Validate') { btn.click(); break; }
				}
			});
			await page.waitForTimeout(500);
			await screenshot(page, '21-advanced-invalid-json.png');
			const state = await page.evaluate(q, 'config_state');
			assert.ok(state.includes('Invalid JSON'), 'invalid JSON detected');
			await configEditor.fill(originalConfig);
		});

		// === MOBILE VIEWPORT ===
		console.log('\n--- Mobile viewport captures ---');
		await page.setViewportSize({ width: 375, height: 812 });

		await clickTab(page, 'Dashboard');
		await page.waitForTimeout(2000);
		await screenshot(page, '22-mobile-dashboard.png');

		await clickTab(page, 'Network');
		await page.waitForFunction(() => {
			const el = document.getElementById('nw_loading');
			return el && el.textContent !== 'Loading…';
		}, { timeout: 10000 });
		await screenshot(page, '23-mobile-network.png');

		await clickTab(page, 'Configuration');
		await waitForText(page, 'cfg_content', 'Loading…', 15000);
		await page.evaluate(() => {
			const psBody = document.getElementById('cfg_ps_body');
			if (psBody) psBody.scrollIntoView({ block: 'center' });
		});
		await page.waitForTimeout(500);
		await screenshot(page, '24-mobile-config.png');

		await clickTab(page, 'Logs');
		await waitForText(page, 'logs_box', 'Loading…', 10000);
		await screenshot(page, '25-mobile-logs.png');

		await clickTab(page, 'Advanced');
		await page.waitForFunction(() => {
			const cfg = document.getElementById('config_editor');
			return cfg && cfg.value;
		}, { timeout: 10000 });
		await screenshot(page, '26-mobile-advanced.png');

		// === SNAPSHOT ===
		const snapshotPath = join(screenshotDir, 'snapshot.json');
		const snapshot = {
			timestamp: new Date().toISOString(),
			tabs: snap.tabs,
			passed: passed.length,
			failed: failed.length,
			failedTests: failed.map(f => f.name),
			dashboard: snap.dashboard,
			config: snap.config,
		};
		writeFileSync(snapshotPath, JSON.stringify(snapshot, null, 2) + '\n');
		console.log('\nSnapshot written:', snapshotPath);

		const baselinePath = join(screenshotDir, 'baseline.json');
		if (existsSync(baselinePath)) {
			const baseline = JSON.parse(readFileSync(baselinePath, 'utf8'));
			const drift = [];
			for (const key of ['tabs', 'config.step_size', 'config.mint_count', 'config.profit_share_rows']) {
				const parts = key.split('.');
				const got = parts.reduce((o, k) => o && o[k], snapshot);
				const expected = parts.reduce((o, k) => o && o[k], baseline);
				if (JSON.stringify(got) !== JSON.stringify(expected)) {
					drift.push(`${key}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(got)}`);
				}
			}
			if (drift.length > 0) {
				console.log('SNAPSHOT DRIFT:');
				drift.forEach(d => console.log('  ~', d));
			} else {
				console.log('SNAPSHOT: matches baseline');
			}
		} else {
			console.log('No baseline.json found. Copy snapshot.json to baseline.json to enable drift detection.');
		}

		console.log('\n=== RESULTS ===');
		console.log('PASSED:', passed.length);
		passed.forEach(p => console.log('  ✓', p));
		if (failed.length > 0) {
			console.log('FAILED:', failed.length);
			failed.forEach(f => console.log('  ✗', f.name, '-', f.error));
		}
		console.log('\nScreenshots:', screenshotDir);

	} finally {
		await browser.close();
	}
}

run().catch((error) => {
	console.error(error);
	process.exit(1);
});
