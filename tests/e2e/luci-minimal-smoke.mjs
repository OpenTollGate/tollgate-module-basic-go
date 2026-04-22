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

async function reloadFiles(page) {
	await page.getByRole('button', { name: 'Reload both files' }).click();
	await page.waitForTimeout(800);
}

async function saveEditor(page, editorSelector, buttonName, object) {
	await page.locator(editorSelector).fill(JSON.stringify(object, null, 2));
	await page.getByRole('button', { name: buttonName }).click();
	await page.waitForTimeout(800);
}

async function run() {
	const browser = await chromium.launch({ headless: true });
	const page = await browser.newPage();

	try {
		await page.goto(url, { waitUntil: 'networkidle' });
		await loginIfNeeded(page);
		await page.waitForFunction(() => {
			const version = document.querySelector('#version_text')?.textContent || '';
			const status = document.querySelector('#status_text')?.textContent || '';
			const wallet = document.querySelector('#wallet_balance')?.textContent || '';
			return version.trim() && version !== 'Loading…' && status.trim() && status !== 'Loading…' && wallet.trim() && wallet !== '—';
		});

		await page.getByRole('button', { name: 'Dashboard' }).waitFor();
		assert.match(await page.locator('#wallet_balance').textContent(), /\S/);
		assert.match(await page.locator('#version_text').textContent(), /version:/i);
		assert.match(await page.locator('#status_text').textContent(), /running:/i);
		assert.match(await page.locator('#logs_box').textContent(), /tollgate-wrt|Skipping payout|No tollgate-wrt/i);

		await page.getByRole('button', { name: 'JSON files' }).click();
		const configEditor = page.locator('#config_editor');
		const identitiesEditor = page.locator('#identities_editor');

		const originalConfig = JSON.parse(await configEditor.inputValue());
		const originalIdentities = JSON.parse(await identitiesEditor.inputValue());

		const configProbe = { ...originalConfig, config_version: `${originalConfig.config_version}-pw` };
		await saveEditor(page, '#config_editor', 'Save config.json', configProbe);
		await reloadFiles(page);
		assert.equal(JSON.parse(await configEditor.inputValue()).config_version, configProbe.config_version);

		await saveEditor(page, '#config_editor', 'Save config.json', originalConfig);
		await reloadFiles(page);
		assert.equal(JSON.parse(await configEditor.inputValue()).config_version, originalConfig.config_version);

		const identitiesProbe = { ...originalIdentities, config_version: `${originalIdentities.config_version}-pw` };
		await saveEditor(page, '#identities_editor', 'Save identities.json', identitiesProbe);
		await reloadFiles(page);
		assert.equal(JSON.parse(await identitiesEditor.inputValue()).config_version, identitiesProbe.config_version);

		await saveEditor(page, '#identities_editor', 'Save identities.json', originalIdentities);
		await reloadFiles(page);
		assert.equal(JSON.parse(await identitiesEditor.inputValue()).config_version, originalIdentities.config_version);

		console.log(JSON.stringify({ ok: true, url, walletBalance: await page.locator('#wallet_balance').textContent() }));
	} finally {
		await browser.close();
	}
}

run().catch((error) => {
	console.error(error);
	process.exit(1);
});
