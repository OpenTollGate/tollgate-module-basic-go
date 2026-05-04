import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdirSync, existsSync } from 'node:fs';
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
	if (!(await page.getByText('Authorization Required').count())) return;
	await page.getByRole('textbox', { name: 'Username' }).fill(username);
	await page.getByRole('textbox', { name: 'Password' }).fill(password);
	await page.getByRole('button', { name: 'Log in' }).click();
	await page.getByRole('heading', { name: 'TollGate' }).waitFor();
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

async function run() {
	const browser = await chromium.launch({ headless: true });
	const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });

	try {
		await page.goto(url, { waitUntil: 'networkidle' });
		await loginIfNeeded(page);
		await screenshot(page, '00-login.png');

		await page.getByRole('button', { name: 'Dashboard' }).waitFor();
		await waitForText(page, 'ov_balance', '—', 15000);
		await waitForText(page, 'ov_version', '', 10000);

		const balance = await page.evaluate(q, 'ov_balance');
		assert.match(balance, /\S/, 'dashboard balance non-empty');
		const version = await page.evaluate(q, 'ov_version');
		assert.match(version, /\S/, 'dashboard version non-empty');
		console.log('PASS: dashboard tab');
		await screenshot(page, '01-dashboard.png');

		await page.getByRole('button', { name: 'Network' }).click();
		await page.waitForFunction(() => {
			const el = document.getElementById('nw_loading');
			return el && el.textContent !== 'Loading…';
		}, { timeout: 10000 });
		const nwText = await page.evaluate(q, 'nw_loading');
		assert.ok(nwText.length > 0, 'network section loaded');
		console.log('PASS: network tab');
		await screenshot(page, '02-network.png');

		await page.getByRole('button', { name: 'Configuration' }).click();
		await waitForText(page, 'cfg_content', 'Loading…', 15000);

		const stepSize = await page.evaluate(qValue, 'cfg_step_size');
		assert.ok(stepSize.length > 0, 'config: step_size field populated');
		console.log('PASS: config tab (schema fields loaded)');
		await screenshot(page, '03-config.png');

		const originalStepSize = await page.evaluate(qValue, 'cfg_step_size');
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
			return el && el.textContent.includes('Saved');
		}, { timeout: 10000 });
		console.log('PASS: config save (step_size probe)');
		await screenshot(page, '04-config-saved.png');

		await page.getByRole('button', { name: 'Dashboard' }).click();
		await page.waitForTimeout(500);
		await page.getByRole('button', { name: 'Configuration' }).click();
		await waitForText(page, 'cfg_content', 'Loading…', 15000);

		const reloadedStepSize = await page.evaluate(qValue, 'cfg_step_size');
		assert.equal(reloadedStepSize, probeValue, 'config round-trip: step_size persisted');

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
		await page.waitForFunction(() => {
			const el = document.getElementById('cfg_save_state');
			return el && el.textContent.includes('Saved');
		}, { timeout: 10000 });
		console.log('PASS: config restore (step_size reverted)');
		await screenshot(page, '05-config-restored.png');

		await page.getByRole('button', { name: 'Logs' }).click();
		await waitForText(page, 'logs_box', 'Loading…', 10000);
		const logs = await page.evaluate(q, 'logs_box');
		assert.ok(logs.length > 0, 'logs loaded');
		console.log('PASS: logs tab');
		await screenshot(page, '06-logs.png');

		await page.getByRole('button', { name: 'Advanced' }).click();
		await page.waitForFunction(() => {
			const cfg = document.getElementById('config_editor');
			const ids = document.getElementById('identities_editor');
			return cfg && cfg.value && ids && ids.value;
		}, { timeout: 10000 });

		const configEditor = page.locator('#config_editor');
		const identitiesEditor = page.locator('#identities_editor');

		const originalConfig = JSON.parse(await configEditor.inputValue());
		const originalIdentities = JSON.parse(await identitiesEditor.inputValue());

		await screenshot(page, '07-advanced.png');

		const configProbe = { ...originalConfig, config_version: `${originalConfig.config_version}-pw` };
		await configEditor.fill(JSON.stringify(configProbe, null, 2));
		await page.getByRole('button', { name: 'Validate' }).first().click();
		await page.waitForTimeout(300);
		await page.getByRole('button', { name: 'Save config.json' }).click();
		await page.waitForFunction(() => {
			const el = document.getElementById('config_state');
			return el && el.textContent.includes('Saved');
		}, { timeout: 5000 });

		await page.getByRole('button', { name: 'Reload both files' }).click();
		await page.waitForFunction(() => {
			const cfg = document.getElementById('config_editor');
			return cfg && cfg.value;
		}, { timeout: 10000 });
		assert.equal(JSON.parse(await configEditor.inputValue()).config_version, configProbe.config_version);
		console.log('PASS: advanced config.json round-trip');

		await configEditor.fill(JSON.stringify(originalConfig, null, 2));
		await page.waitForTimeout(300);
		await page.evaluate(() => {
			const el = document.getElementById('config_state');
			if (el) el.textContent = '';
		});
		await page.waitForTimeout(200);
		await page.getByRole('button', { name: 'Save config.json' }).click();
		try {
			await page.waitForFunction(() => {
				const el = document.getElementById('config_state');
				return el && el.textContent.includes('Saved');
			}, { timeout: 5000 });
		} catch (e) {
			await page.getByRole('button', { name: 'Save config.json' }).click();
			await page.waitForFunction(() => {
				const el = document.getElementById('config_state');
				return el && el.textContent.includes('Saved');
			}, { timeout: 10000 });
		}

		const idProbe = Array.isArray(originalIdentities)
			? [...originalIdentities, { test_marker: 'pw' }]
			: { ...originalIdentities, test_marker: 'pw' };
		await identitiesEditor.fill(JSON.stringify(idProbe, null, 2));
		await page.evaluate(() => {
			const btn = document.querySelector('#identities_editor')
				?.closest('.cbi-section')
				?.querySelector('[class*="cbi-button-action"]');
			if (btn) btn.click();
		});
		await page.waitForTimeout(300);
		await page.getByRole('button', { name: 'Save identities.json' }).click();
		await page.waitForFunction(() => {
			const el = document.getElementById('identities_state');
			return el && el.textContent.includes('Saved');
		}, { timeout: 5000 });

		await page.getByRole('button', { name: 'Reload both files' }).click();
		await page.waitForFunction(() => {
			const ids = document.getElementById('identities_editor');
			return ids && ids.value;
		}, { timeout: 10000 });
		const reloadedIdentities = JSON.parse(await identitiesEditor.inputValue());
		if (Array.isArray(idProbe)) {
			assert.equal(reloadedIdentities.length, idProbe.length);
		} else {
			assert.equal(reloadedIdentities.test_marker, 'pw');
		}
		console.log('PASS: advanced identities.json round-trip');

		await identitiesEditor.fill(JSON.stringify(originalIdentities, null, 2));
		await page.waitForTimeout(300);
		await page.evaluate(() => {
			const el = document.getElementById('identities_state');
			if (el) el.textContent = '';
		});
		await page.getByRole('button', { name: 'Save identities.json' }).click();
		try {
			await page.waitForFunction(() => {
				const el = document.getElementById('identities_state');
				return el && el.textContent.includes('Saved');
			}, { timeout: 5000 });
		} catch (e) {
			await page.getByRole('button', { name: 'Save identities.json' }).click();
			await page.waitForFunction(() => {
				const el = document.getElementById('identities_state');
				return el && el.textContent.includes('Saved');
			}, { timeout: 10000 });
		}

		console.log(JSON.stringify({
			ok: true,
			url,
			walletBalance: await page.evaluate(q, 'ov_balance'),
			tabs: ['dashboard', 'network', 'config', 'logs', 'advanced']
		}));
	} finally {
		await browser.close();
	}
}

run().catch((error) => {
	console.error(error);
	process.exit(1);
});
