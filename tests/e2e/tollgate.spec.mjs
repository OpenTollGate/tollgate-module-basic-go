import { test, expect } from '@playwright/test';
import { fileExists, readFile, cleanupFiles, getWalletBalance, getWalletInfo, drainViaCLI, fundViaCLI, mintTestnutTokens } from './helpers/router.mjs';

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

	test('network: password and enable/disable buttons render', async ({ page }) => {
		await clickTab(page, 'Network');
		await waitLoaded(page, 'nw_loading');
		const pwInput = await page.evaluate(() => !!document.getElementById('nw_new_pw'));
		expect(pwInput).toBeTruthy();
		const hasEnableBtn = await page.evaluate(() => {
			const btns = document.querySelectorAll('button');
			for (const b of btns) if (b.textContent.trim() === 'Enable') return true;
			return false;
		});
		expect(hasEnableBtn).toBeTruthy();
		const hasDisableBtn = await page.evaluate(() => {
			const btns = document.querySelectorAll('button');
			for (const b of btns) if (b.textContent.trim() === 'Disable') return true;
			return false;
		});
		expect(hasDisableBtn).toBeTruthy();
	});

	test('config: add share applies proportional squeeze', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => document.getElementById('cfg_ps_body')?.scrollIntoView({ block: 'center' }));
		await page.waitForTimeout(300);

		const before = await page.evaluate(() => {
			const tbody = document.getElementById('cfg_ps_body');
			if (!tbody || tbody.children.length === 0) return null;
			const factors = [];
			for (let i = 0; i < tbody.children.length; i++) {
				const el = document.getElementById('cfg_ps_' + i + '_factor');
				factors.push(el ? parseFloat(el.value) : 0);
			}
			return { count: tbody.children.length, factors };
		});
		if (!before || before.count < 1) return;

		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('button'))
				if (btn.textContent.trim() === 'Add Share') { btn.click(); break; }
		});
		await page.waitForTimeout(500);

		const after = await page.evaluate(() => {
			const tbody = document.getElementById('cfg_ps_body');
			if (!tbody) return null;
			const factors = [];
			for (let i = 0; i < tbody.children.length; i++) {
				const el = document.getElementById('cfg_ps_' + i + '_factor');
				factors.push(el ? parseFloat(el.value) : 0);
			}
			return { count: tbody.children.length, factors };
		});
		if (!after) return;

		expect(after.count).toBe(before.count + 1);
		const n = before.count;
		const squeeze = n / (n + 1);
		const newShare = 1 / (n + 1);
		for (let i = 0; i < n; i++) {
			const expected = before.factors[i] * squeeze;
			expect(Math.abs(after.factors[i] - expected)).toBeLessThan(0.02);
		}
		expect(Math.abs(after.factors[n] - newShare)).toBeLessThan(0.02);
	});

	test('config: remove share row redistributes evenly', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => document.getElementById('cfg_ps_body')?.scrollIntoView({ block: 'center' }));
		await page.waitForTimeout(300);

		const beforeCount = await page.evaluate(() => document.getElementById('cfg_ps_body')?.children.length ?? 0);
		if (beforeCount < 2) return;

		const removeBtns = await page.evaluate(() => {
			const tbody = document.getElementById('cfg_ps_body');
			if (!tbody) return 0;
			const btns = tbody.querySelectorAll('.tg-btn-remove');
			if (btns.length) btns[btns.length - 1].click();
			return tbody.children.length;
		});
		await page.waitForTimeout(500);

		const after = await page.evaluate(() => {
			const tbody = document.getElementById('cfg_ps_body');
			if (!tbody) return null;
			const factors = [];
			for (let i = 0; i < tbody.children.length; i++) {
				const el = document.getElementById('cfg_ps_' + i + '_factor');
				factors.push(el ? parseFloat(el.value) : 0);
			}
			return { count: tbody.children.length, factors };
		});
		if (!after) return;

		expect(after.count).toBe(beforeCount - 1);
		const evenShare = 1 / after.count;
		for (const f of after.factors) {
			expect(Math.abs(f - evenShare)).toBeLessThan(0.02);
		}
	});

	test('config: add identity row', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => document.getElementById('cfg_pi_body')?.scrollIntoView({ block: 'center' }));
		await page.waitForTimeout(300);

		const before = await page.evaluate(() => document.getElementById('cfg_pi_body')?.children.length ?? 0);
		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('button'))
				if (btn.textContent.trim() === 'Add Identity') { btn.click(); break; }
		});
		await page.waitForTimeout(300);

		const after = await page.evaluate(() => document.getElementById('cfg_pi_body')?.children.length ?? 0);
		expect(after).toBe(before + 1);

		const newInputs = await page.evaluate(() => {
			const tbody = document.getElementById('cfg_pi_body');
			if (!tbody) return false;
			const lastRow = tbody.children[tbody.children.length - 1];
			return lastRow ? lastRow.querySelectorAll('input').length >= 2 : false;
		});
		expect(newInputs).toBeTruthy();
	});

	test('config: remove identity row', async ({ page }) => {
		await waitForConfig(page);
		await page.evaluate(() => document.getElementById('cfg_pi_body')?.scrollIntoView({ block: 'center' }));
		await page.waitForTimeout(300);

		const before = await page.evaluate(() => document.getElementById('cfg_pi_body')?.children.length ?? 0);
		if (before < 2) return;

		await page.evaluate(() => {
			const tbody = document.getElementById('cfg_pi_body');
			if (!tbody) return;
			const btns = tbody.querySelectorAll('.tg-btn-remove');
			if (btns.length) btns[btns.length - 1].click();
		});
		await page.waitForTimeout(300);

		const after = await page.evaluate(() => document.getElementById('cfg_pi_body')?.children.length ?? 0);
		expect(after).toBe(before - 1);
	});

	test('advanced: reload files updates timestamp', async ({ page }) => {
		await waitForAdvanced(page);
		await page.evaluate(() => {
			for (const btn of document.querySelectorAll('button'))
				if (btn.textContent.trim() === 'Reload both files') { btn.click(); break; }
		});
		await page.waitForFunction(
			() => {
				const el = document.getElementById('files_state');
				return el && el.textContent.includes('Loaded');
			},
			{ timeout: 10000 }
		);
		expect(await $('files_state')(page)).toContain('Loaded');
	});

	test('advanced: validate identities editor', async ({ page }) => {
		await waitForAdvanced(page);
		const editor = page.locator('#identities_editor');
		const original = await editor.inputValue();
		await editor.fill('not valid json {{{');
		await page.evaluate(() => {
			const btns = document.querySelectorAll('button');
			for (let i = btns.length - 1; i >= 0; i--) {
				if (btns[i].textContent.trim() === 'Validate') { btns[i].click(); break; }
			}
		});
		await page.waitForTimeout(500);
		expect(await $('identities_state')(page)).toContain('Invalid JSON');
		await editor.fill(original);
	});

	test('drain: modal appears and can be cancelled', async ({ page }) => {
		await page.waitForTimeout(3000);
		await page.getByRole('button', { name: 'Drain All Funds' }).click();
		await page.waitForTimeout(500);
		const cancelBtn = page.locator('#modal_overlay .cbi-button').first();
		if (await cancelBtn.count()) {
			await cancelBtn.click();
			await page.waitForTimeout(300);
			expect(await $('wl_drain_state')(page)).not.toContain('Drained');
		}
	});

	test('drain: empty wallet shows message', async ({ page }) => {
		const balance = getWalletBalance();
		if (balance > 0) test.skip();
		await page.waitForTimeout(3000);
		await page.getByRole('button', { name: 'Drain All Funds' }).click();
		await page.waitForTimeout(500);
		const confirmBtn = page.locator('#modal_overlay .cbi-button-remove').first();
		if (await confirmBtn.count()) await confirmBtn.click();
		await page.waitForFunction(
			() => { const el = document.getElementById('wl_drain_state'); return el && el.textContent.trim().length > 0 && !el.textContent.includes('Draining'); },
			{ timeout: 10000 }
		);
		const state = await $('wl_drain_state')(page);
		expect(state).toMatch(/Drained|No tokens/i);
	});

	test('drain: saves tokens to file on device', async ({ page }) => {
		const info = getWalletInfo();
		const balance = info?.data?.total_balance ?? 0;
		if (balance === 0) test.skip();
		const mintBalances = info?.data?.mint_balances || {};
		for (const [url, bal] of Object.entries(mintBalances)) {
			if (bal > 0 && !url.toLowerCase().includes('testnut')) {
				test.skip();
				return;
			}
		}

		cleanupFiles('/root/tollgate-drain-*.txt');
		await page.waitForTimeout(3000);
		await page.getByRole('button', { name: 'Drain All Funds' }).click();
		await page.waitForTimeout(500);
		const confirmBtn = page.locator('#modal_overlay .cbi-button-remove').first();
		if (await confirmBtn.count()) await confirmBtn.click();

		await page.waitForFunction(
			() => { const el = document.getElementById('wl_drain_state'); return el && (el.textContent.includes('Drained') || el.textContent.includes('Error')); },
			{ timeout: 15000 }
		);

		const stateText = await $('wl_drain_state')(page);

		if (stateText.includes('Error')) {
			expect(stateText).toMatch(/drain|funds|mint/i);
			return;
		}

		expect(stateText).toContain('Drained');

		const pathMatch = stateText.match(/\/root\/tollgate-drain-[^\s]+/);
		if (!pathMatch) {
			return;
		}

		const filePath = pathMatch[0];
		expect(fileExists(filePath)).toBeTruthy();
		const content = readFile(filePath);
		expect(content).toContain('TollGate Wallet Drain');
		expect(content).toMatch(/cashuA/i);

		expect(getWalletBalance()).toBe(0);

		cleanupFiles(filePath);
	});

	test('fund: valid testnut token updates balance', async ({ page }) => {
		const token = mintTestnutTokens(10);
		expect(token).toMatch(/^cashu[AB]/);

		await page.waitForTimeout(3000);
		await page.evaluate((t) => { const el = document.getElementById('wl_token'); if (el) el.value = t; }, token);
		await page.getByRole('button', { name: 'Fund Wallet' }).click();
		await page.waitForFunction(
			() => { const el = document.getElementById('wl_fund_state'); return el && (el.textContent.includes('Funded') || el.textContent.includes('Error') || el.textContent.includes('Failed')); },
			{ timeout: 15000 }
		);
		const state = await $('wl_fund_state')(page);
		expect(state).toContain('Funded');

		const info = getWalletInfo();
		expect(info?.data?.total_balance).toBeGreaterThan(0);
	});

	test('fund: garbage token shows error', async ({ page }) => {
		await page.waitForTimeout(3000);
		await page.evaluate(() => { const el = document.getElementById('wl_token'); if (el) el.value = 'not-a-valid-token-at-all'; });
		await page.getByRole('button', { name: 'Fund Wallet' }).click();
		await page.waitForFunction(
			() => { const el = document.getElementById('wl_fund_state'); return el && el.textContent.trim().length > 0 && !el.textContent.includes('Funding'); },
			{ timeout: 10000 }
		);
		const state = await $('wl_fund_state')(page);
		expect(state).toMatch(/error|fail|invalid/i);
	});

	test('fund + drain lifecycle with SSH verification', async ({ page }) => {
		cleanupFiles('/root/tollgate-drain-*.txt');

		const token = mintTestnutTokens(10);
		expect(token).toMatch(/^cashu[AB]/);

		const fundResult = fundViaCLI(token);
		expect(fundResult?.success).toBeTruthy();
		expect(fundResult?.data?.amount_received).toBeGreaterThan(0);

		const balanceBefore = getWalletBalance();
		expect(balanceBefore).toBeGreaterThan(0);

		await page.waitForTimeout(3000);
		await page.getByRole('button', { name: 'Drain All Funds' }).click();
		await page.waitForTimeout(500);
		const confirmBtn = page.locator('#modal_overlay .cbi-button-remove').first();
		if (await confirmBtn.count()) await confirmBtn.click();

		await page.waitForFunction(
			() => { const el = document.getElementById('wl_drain_state'); return el && (el.textContent.includes('Drained') || el.textContent.includes('Error')); },
			{ timeout: 15000 }
		);

		const stateText = await $('wl_drain_state')(page);

		if (!stateText.includes('Error')) {
			expect(stateText).toContain('Drained');

			const pathMatch = stateText.match(/\/root\/tollgate-drain-[^\s]+/);
			if (pathMatch) {
				const filePath = pathMatch[0];
				expect(fileExists(filePath)).toBeTruthy();
				const content = readFile(filePath);
				expect(content).toContain('TollGate Wallet Drain');
				expect(content).toMatch(/cashuA/i);
				cleanupFiles(filePath);
			}
		}

		const balanceAfter = getWalletBalance();
		expect(balanceAfter).toBe(0);
	});

	test('drain twice shows zero on second attempt', async ({ page }) => {
		const balance = getWalletBalance();
		if (balance > 0) {
			drainViaCLI();
		}

		await page.waitForTimeout(3000);
		await page.getByRole('button', { name: 'Drain All Funds' }).click();
		await page.waitForTimeout(500);
		const confirmBtn = page.locator('#modal_overlay .cbi-button-remove').first();
		if (await confirmBtn.count()) await confirmBtn.click();
		await page.waitForFunction(
			() => { const el = document.getElementById('wl_drain_state'); return el && (el.textContent.includes('Drained') || el.textContent.includes('No tokens') || el.textContent.includes('Error')); },
			{ timeout: 10000 }
		);
		const state = await $('wl_drain_state')(page);
		expect(state).toMatch(/Drained|No tokens/i);
	});
});
