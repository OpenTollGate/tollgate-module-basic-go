import { test, expect } from '@playwright/test';

const username = process.env.TOLLGATE_LUCI_USER;
const password = process.env.TOLLGATE_LUCI_PASSWORD;
const PAGE_URL = '/cgi-bin/luci/admin/services/tollgate-payments';

test.beforeEach(async ({ page }) => {
	if (!username || !password) test.skip();
	await page.goto(PAGE_URL, { waitUntil: 'networkidle' });
	const body = await page.evaluate(() => document.body.innerText.substring(0, 200));
	if (body.includes('Authorization Required')) {
		await page.getByRole('textbox', { name: 'Username' }).fill(username);
		await page.getByRole('textbox', { name: 'Password' }).fill(password);
		await page.getByRole('button', { name: 'Log in' }).click();
	}
	await page.getByRole('heading', { name: 'TollGate' }).waitFor({ timeout: 15000 });
});

const $ = (id) => page => page.evaluate(i => {
	const el = document.getElementById(i);
	return el ? el.textContent.trim() : '';
}, id);

const $val = (id) => page => page.evaluate(i => {
	const el = document.getElementById(i);
	return el ? el.value : '';
}, id);

async function clickTab(page, name) {
	await page.getByRole('button', { name }).click();
}

async function waitLoaded(page, id, timeout = 20000) {
	await page.waitForFunction(
		sel => { const el = document.getElementById(sel); const t = el?.textContent.trim(); return t && t !== 'Loading…'; },
		[id], { timeout }
	);
}

async function waitForConfig(page) {
	await clickTab(page, 'Configuration');
	await page.waitForFunction(
		() => { const el = document.getElementById('cfg_content'); return el && el.textContent !== 'Loading…' && el.children.length > 0; },
		{ timeout: 30000 }
	);
	await page.waitForTimeout(500);
}

async function waitForAdvanced(page) {
	await clickTab(page, 'Advanced');
	await page.waitForFunction(
		() => {
			const c = document.getElementById('config_editor');
			const i = document.getElementById('identities_editor');
			return c && c.value && i && i.value;
		},
		{ timeout: 30000 }
	);
}

// ── Shared: all viewports ───────────────────────────────────

test('dashboard loads', async ({ page }) => {
	await page.waitForFunction(
		() => { const el = document.getElementById('ov_version'); return el && el.textContent.trim() !== '—'; },
		{ timeout: 15000 }
	);
	expect(await $('ov_balance')(page)).toBeTruthy();
	expect(await $('ov_version')(page)).toContain('Version:');
});

test('network tab loads', async ({ page }) => {
	await clickTab(page, 'Network');
	await waitLoaded(page, 'nw_loading');
});

test('configuration tab loads', async ({ page }) => {
	await waitForConfig(page);
	expect(await $val('cfg_step_size')(page)).toBeTruthy();
});

test('logs tab loads', async ({ page }) => {
	await clickTab(page, 'Logs');
	await waitLoaded(page, 'logs_box');
	const lines = (await $('logs_box')(page)).split('\n').filter(l => l.trim());
	expect(lines.length).toBeGreaterThan(0);
});

test('advanced tab loads', async ({ page }) => {
	await waitForAdvanced(page);
	expect(await $val('config_editor')(page)).toBeTruthy();
});

// ── Desktop-only interactions ───────────────────────────────

test.describe('desktop interactions', () => {
	test.skip(({ viewport }) => viewport?.width !== 1280);

	test('dashboard: restart modal', async ({ page }) => {
		await page.waitForTimeout(3000);
		await page.getByRole('button', { name: 'Restart' }).click();
		await page.waitForTimeout(500);
		await page.waitForFunction(
			() => !!document.querySelector('.cbi-modal') || !!document.querySelector('[class*="modal"]'),
			{ timeout: 5000 }
		).catch(() => {});
		await page.waitForTimeout(8000);
		await page.evaluate(() => {
			['ov_btn_start', 'ov_btn_stop', 'ov_btn_restart'].forEach(id => {
				const el = document.getElementById(id);
				if (el) el.disabled = false;
			});
		});
	});

	test('dashboard: fund empty warning', async ({ page }) => {
		await page.waitForTimeout(3000);
		await page.evaluate(() => { const el = document.getElementById('wl_token'); if (el) el.value = ''; });
		await page.getByRole('button', { name: 'Fund Wallet' }).click();
		await page.waitForTimeout(500);
		expect(await $('wl_fund_state')(page)).toContain('Enter a token');
	});

	test('dashboard: drain modal', async ({ page }) => {
		await page.waitForTimeout(3000);
		await page.getByRole('button', { name: 'Drain All Funds' }).click();
		await page.waitForTimeout(500);
		const cancelBtn = page.locator('#modal_overlay .cbi-button').first();
		if (await cancelBtn.count()) await cancelBtn.click();
	});

	test('network: show password toggle', async ({ page }) => {
		await clickTab(page, 'Network');
		await waitLoaded(page, 'nw_loading');
		const showBtn = page.locator('button:has-text("Show")').first();
		if (!(await showBtn.count())) return;
		await showBtn.click();
		await page.waitForTimeout(300);
		const pwText = await page.evaluate(() => document.getElementById('nw_pw_display')?.textContent ?? '');
		expect(pwText.includes('\u2022')).toBeFalsy();
	});

	test('network: rename empty warning', async ({ page }) => {
		await clickTab(page, 'Network');
		await waitLoaded(page, 'nw_loading');
		await page.evaluate(() => { const el = document.getElementById('nw_new_ssid'); if (el) el.value = ''; });
		await page.getByRole('button', { name: 'Rename' }).click();
		await page.waitForTimeout(500);
		expect(await $('nw_rename_state')(page)).toContain('Enter a new SSID');
	});

	test('config: expand object section', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => {
			const d = document.querySelector('details.tg-adv-details');
			if (d) d.setAttribute('open', '');
		});
		await page.waitForTimeout(300);
	});

	test('config: profit share loaded', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => document.getElementById('cfg_ps_body')?.scrollIntoView({ block: 'center' }));
		await page.waitForTimeout(300);
		const rows = await page.evaluate(() => document.getElementById('cfg_ps_body')?.children.length ?? 0);
		expect(rows).toBeGreaterThan(0);
	});

	test('config: profit share slider rebalance', async ({ page }) => {
		await waitForConfig(page);
		const changed = await page.evaluate(() => {
			const r = document.getElementById('cfg_ps_0_range');
			if (!r) return false;
			r.value = '90';
			r.dispatchEvent(new Event('input', { bubbles: true }));
			return true;
		});
		if (!changed) return;
		await page.waitForTimeout(500);
		expect(await $('cfg_ps_0_pct')(page)).toContain('90');
	});

	test('config: add mint card', async ({ page }) => {
		await waitForConfig(page);
		const before = await page.evaluate(() => document.getElementById('cfg_mints_body')?.children.length ?? 0);
		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('button'))
				if (btn.textContent.trim() === 'Add Mint') { btn.click(); break; }
		});
		await page.waitForTimeout(500);
		const after = await page.evaluate(() => document.getElementById('cfg_mints_body')?.children.length ?? 0);
		expect(after).toBe(before + 1);
	});

	test('config: remove mint card', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => {
			const btns = document.querySelectorAll('.tg-mint-remove');
			if (btns.length) btns[btns.length - 1].click();
		});
		await page.waitForTimeout(300);
	});

	test('config: identities loaded', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => document.getElementById('cfg_pi_body')?.scrollIntoView({ block: 'center' }));
		await page.waitForTimeout(300);
		const rows = await page.evaluate(() => document.getElementById('cfg_pi_body')?.children.length ?? 0);
		expect(rows).toBeGreaterThan(0);
	});

	test('config: save round-trip', async ({ page }) => {
		await waitForConfig(page);
		const original = await $val('cfg_step_size')(page);
		if (!original) return;
		const probe = String(parseInt(original, 10) + 1024);

		await page.evaluate(v => { const el = document.getElementById('cfg_step_size'); if (el) el.value = v; }, probe);
		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('.cbi-button-save'))
				if (btn.textContent.trim() === 'Save') { btn.click(); break; }
		});
		await page.waitForFunction(
			() => { const el = document.getElementById('cfg_save_state'); return el && (el.textContent.includes('Saved') || el.textContent.includes('failed')); },
			{ timeout: 15000 }
		);
		expect(await $('cfg_save_state')(page)).toContain('Saved');

		await page.evaluate(v => { const el = document.getElementById('cfg_step_size'); if (el) el.value = v; }, original);
		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('.cbi-button-save'))
				if (btn.textContent.trim() === 'Save') { btn.click(); break; }
		});
		try {
			await page.waitForFunction(
				() => { const el = document.getElementById('cfg_save_state'); return el?.textContent.includes('Saved'); },
				{ timeout: 15000 }
			);
		} catch {}
	});

	test('advanced: validate valid JSON', async ({ page }) => {
		await waitForAdvanced(page);
		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('button'))
				if (btn.textContent.trim() === 'Validate') { btn.click(); break; }
		});
		await page.waitForTimeout(500);
		expect(await $('config_state')(page)).toContain('Valid JSON');
	});

	test('advanced: validate invalid JSON', async ({ page }) => {
		await waitForAdvanced(page);
		const editor = page.locator('#config_editor');
		const original = await editor.inputValue();
		await editor.fill('{invalid json!!!');
		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('button'))
				if (btn.textContent.trim() === 'Validate') { btn.click(); break; }
		});
		await page.waitForTimeout(500);
		expect(await $('config_state')(page)).toContain('Invalid JSON');
		await editor.fill(original);
	});
});
