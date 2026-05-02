import assert from 'node:assert/strict';
import { chromium } from 'playwright';

const router = process.env.TOLLGATE_ROUTER ?? '192.168.13.202:8080';
const username = process.env.TOLLGATE_LUCI_USER ?? 'root';
const password = process.env.TOLLGATE_LUCI_PASSWORD ?? 'c03rad0r123';
const url = process.env.TOLLGATE_LUCI_URL ?? `http://${router}/cgi-bin/luci/admin/services/tollgate-payments`;

async function loginIfNeeded(page) {
	if (!(await page.getByText('Authorization Required').count())) return;
	await page.getByRole('textbox', { name: 'Username' }).fill(username);
	await page.getByRole('textbox', { name: 'Password' }).fill(password);
	await page.getByRole('button', { name: 'Log in' }).click();
	await page.getByRole('heading', { name: 'TollGate' }).waitFor();
}

async function waitForOverview(page) {
	await page.waitForFunction(() => {
		const balance = document.querySelector('#ov_balance')?.textContent || '';
		const version = document.querySelector('#ov_version')?.textContent || '';
		return balance.trim() && balance !== '—' && balance !== 'Loading…' && version.trim() && version !== 'Loading…';
	}, { timeout: 15000 });
}

async function run() {
	const browser = await chromium.launch({ headless: true });
	const page = await browser.newPage();

	try {
		await page.goto(url, { waitUntil: 'networkidle' });
		await loginIfNeeded(page);

		await page.getByRole('button', { name: 'Overview' }).waitFor();
		await waitForOverview(page);

		assert.match(await page.locator('#ov_balance').textContent(), /\S/);
		assert.match(await page.locator('#ov_version').textContent(), /\S/);

		await page.getByRole('button', { name: 'Wallet' }).click();
		await page.waitForFunction(() => {
			const b = document.querySelector('#wl_balance')?.textContent || '';
			return b.trim() && b !== 'Loading…';
		}, { timeout: 10000 });
		assert.match(await page.locator('#wl_balance').textContent(), /\S/);
		assert.ok(await page.locator('#wl_info').textContent(), 'wallet info loaded');

		await page.getByRole('button', { name: 'Network' }).click();
		await page.waitForTimeout(2000);
		var nwLoading = await page.locator('#nw_loading').isVisible();
		if (!nwLoading) {
			var nwText = await page.locator('#nw_loading').textContent();
			assert.ok(nwText && nwText.trim().length > 0, 'network section loaded');
		}

		await page.getByRole('button', { name: 'Configuration' }).click();
		await page.waitForTimeout(1500);
		var cfgContent = await page.locator('#cfg_content').textContent();
		assert.ok(cfgContent && cfgContent.trim().length > 0, 'config section loaded');

		await page.getByRole('button', { name: 'Logs' }).click();
		await page.waitForFunction(() => {
			const el = document.querySelector('#logs_box');
			return el && el.textContent !== 'Loading…';
		}, { timeout: 10000 });
		assert.ok(await page.locator('#logs_box').textContent(), 'logs loaded');

		await page.getByRole('button', { name: 'Advanced' }).click();
		await page.waitForFunction(() => {
			const cfg = document.querySelector('#config_editor');
			const ids = document.querySelector('#identities_editor');
			return cfg && cfg.value && ids && ids.value;
		}, { timeout: 10000 });
		const configEditor = page.locator('#config_editor');
		const identitiesEditor = page.locator('#identities_editor');

		const originalConfig = JSON.parse(await configEditor.inputValue());
		const originalIdentities = JSON.parse(await identitiesEditor.inputValue());

		const configProbe = { ...originalConfig, config_version: `${originalConfig.config_version}-pw` };
		await configEditor.fill(JSON.stringify(configProbe, null, 2));
		await page.getByRole('button', { name: 'Validate' }).first().click();
		await page.waitForTimeout(300);
		await page.getByRole('button', { name: 'Save config.json' }).click();
		await page.waitForFunction(() => {
			const el = document.querySelector('#config_state');
			return el && el.textContent.includes('Saved');
		}, { timeout: 5000 });

		await page.getByRole('button', { name: 'Reload both files' }).click();
		await page.waitForFunction(() => {
			const el = document.querySelector('#config_editor');
			return el && el.value.length > 0;
		}, { timeout: 5000 });
		assert.equal(JSON.parse(await configEditor.inputValue()).config_version, configProbe.config_version);

		await configEditor.fill(JSON.stringify(originalConfig, null, 2));
		await page.getByRole('button', { name: 'Save config.json' }).click();
		await page.waitForFunction(() => {
			const el = document.querySelector('#config_state');
			return el && el.textContent.includes('Saved');
		}, { timeout: 5000 });

		console.log(JSON.stringify({ ok: true, url, walletBalance: await page.locator('#ov_balance').textContent() }));
	} finally {
		await browser.close();
	}
}

run().catch((error) => {
	console.error(error);
	process.exit(1);
});
