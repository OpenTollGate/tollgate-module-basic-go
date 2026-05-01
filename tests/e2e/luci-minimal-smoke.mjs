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

async function waitForDashboardLoad(page) {
	await page.waitForFunction(() => {
		const balance = document.querySelector('#ov_balance')?.textContent || '';
		const version = document.querySelector('#ov_version')?.textContent || '';
		return balance.trim() && balance !== '—' && version.trim() && version !== 'Loading…';
	});
}

async function run() {
	const browser = await chromium.launch({ headless: true });
	const page = await browser.newPage();

	try {
		await page.goto(url, { waitUntil: 'networkidle' });
		await loginIfNeeded(page);

		await page.getByRole('button', { name: 'Overview' }).waitFor();
		await waitForDashboardLoad(page);

		assert.match(await page.locator('#ov_balance').textContent(), /\S/);
		assert.match(await page.locator('#ov_version').textContent(), /version:/i);

		await page.getByRole('button', { name: 'Wallet' }).click();
		await page.waitForTimeout(1500);
		assert.match(await page.locator('#wl_balance').textContent(), /\S/);
		assert.ok(await page.locator('#wl_info').textContent(), 'wallet info loaded');

		await page.getByRole('button', { name: 'Network' }).click();
		await page.waitForTimeout(1500);
		var nwLoading = await page.locator('#nw_status_loading').isVisible();
		if (!nwLoading) {
			assert.ok(await page.locator('#nw_enabled').textContent(), 'network status loaded');
			assert.ok(await page.locator('#nw_ssid').textContent(), 'SSID loaded');
		}

		await page.getByRole('button', { name: 'Configuration' }).click();
		await page.waitForTimeout(1500);
		var cfgVisible = await page.locator('#cfg_content').isVisible();
		if (cfgVisible) {
			assert.ok(await page.locator('#cfg_price').textContent(), 'price loaded');
			assert.ok(await page.locator('#cfg_metric').textContent(), 'metric loaded');
		}

		await page.getByRole('button', { name: 'Logs' }).click();
		await page.waitForTimeout(1500);
		assert.ok(await page.locator('#logs_box').textContent(), 'logs loaded');

		await page.getByRole('button', { name: 'Advanced' }).click();
		await page.waitForTimeout(1500);
		const configEditor = page.locator('#config_editor');
		const identitiesEditor = page.locator('#identities_editor');

		const originalConfig = JSON.parse(await configEditor.inputValue());
		const originalIdentities = JSON.parse(await identitiesEditor.inputValue());

		const configProbe = { ...originalConfig, config_version: `${originalConfig.config_version}-pw` };
		await configEditor.fill(JSON.stringify(configProbe, null, 2));
		await page.getByRole('button', { name: 'Save config.json' }).click();
		await page.waitForTimeout(800);
		await page.getByRole('button', { name: 'Reload both files' }).click();
		await page.waitForTimeout(800);
		assert.equal(JSON.parse(await configEditor.inputValue()).config_version, configProbe.config_version);

		await configEditor.fill(JSON.stringify(originalConfig, null, 2));
		await page.getByRole('button', { name: 'Save config.json' }).click();
		await page.waitForTimeout(800);
		await page.getByRole('button', { name: 'Reload both files' }).click();
		await page.waitForTimeout(800);
		assert.equal(JSON.parse(await configEditor.inputValue()).config_version, originalConfig.config_version);

		const identitiesProbe = { ...originalIdentities, config_version: `${originalIdentities.config_version}-pw` };
		await identitiesEditor.fill(JSON.stringify(identitiesProbe, null, 2));
		await page.getByRole('button', { name: 'Save identities.json' }).click();
		await page.waitForTimeout(800);
		await page.getByRole('button', { name: 'Reload both files' }).click();
		await page.waitForTimeout(800);
		assert.equal(JSON.parse(await identitiesEditor.inputValue()).config_version, identitiesProbe.config_version);

		await identitiesEditor.fill(JSON.stringify(originalIdentities, null, 2));
		await page.getByRole('button', { name: 'Save identities.json' }).click();
		await page.waitForTimeout(800);
		await page.getByRole('button', { name: 'Reload both files' }).click();
		await page.waitForTimeout(800);
		assert.equal(JSON.parse(await identitiesEditor.inputValue()).config_version, originalIdentities.config_version);

		console.log(JSON.stringify({ ok: true, url, walletBalance: await page.locator('#ov_balance').textContent() }));
	} finally {
		await browser.close();
	}
}

run().catch((error) => {
	console.error(error);
	process.exit(1);
});
