import { test, expect } from '@playwright/test';
import assert from 'node:assert/strict';

const router = process.env.TOLLGATE_ROUTER ?? '192.168.13.202:8080';
const username = process.env.TOLLGATE_LUCI_USER;
const password = process.env.TOLLGATE_LUCI_PASSWORD;
const baseURL = process.env.TOLLGATE_LUCI_URL ?? `http://${router}/cgi-bin/luci/admin/services/tollgate-payments`;

async function loginIfNeeded(page) {
  const authHeading = page.getByText('Authorization Required');
  if (!(await authHeading.count())) return;
  await page.getByRole('textbox', { name: 'Username' }).fill(username);
  await page.getByRole('textbox', { name: 'Password' }).fill(password);
  await page.getByRole('button', { name: 'Log in' }).click();
  await page.getByRole('heading', { name: 'TollGate' }).waitFor();
}

async function waitForText(page, id, notMatching, timeout = 15000) {
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

test.describe('TollGate LuCI Admin UI', () => {
  test.skip(() => !username || !password, 'TOLLGATE_LUCI_USER and TOLLGATE_LUCI_PASSWORD required');

  test.beforeEach(async ({ page }) => {
    await page.goto(baseURL, { waitUntil: 'networkidle' });
    await loginIfNeeded(page);
    await page.getByRole('heading', { name: 'TollGate' }).waitFor();
  });

  test('overview tab loads balance and version', async ({ page }) => {
    await page.getByRole('button', { name: 'Overview' }).waitFor();
    await waitForText(page, 'ov_balance', '—', 15000);
    await waitForText(page, 'ov_version', '', 10000);

    const balance = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.textContent.trim() : '';
    }, 'ov_balance');
    assert.match(balance, /\S/, 'overview balance non-empty');

    const version = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.textContent.trim() : '';
    }, 'ov_version');
    assert.match(version, /\S/, 'overview version non-empty');
  });

  test('overview tab has service control buttons', async ({ page }) => {
    await page.getByRole('button', { name: 'Start' }).waitFor();
    await page.getByRole('button', { name: 'Stop' }).waitFor();
    await page.getByRole('button', { name: 'Restart' }).waitFor();
  });

  test('wallet tab displays balance and info', async ({ page }) => {
    await page.getByRole('button', { name: 'Wallet' }).click();
    await waitForText(page, 'wl_balance', 'Loading…', 10000);

    const wlBalance = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.textContent.trim() : '';
    }, 'wl_balance');
    assert.match(wlBalance, /\S/, 'wallet balance non-empty');
  });

  test('wallet tab has fund and drain controls', async ({ page }) => {
    await page.getByRole('button', { name: 'Wallet' }).click();
    await page.locator('#wl_token').waitFor();
    await page.getByRole('button', { name: 'Fund Wallet' }).waitFor();
    await page.getByRole('button', { name: 'Drain All Funds' }).waitFor();
  });

  test('network tab loads private network info', async ({ page }) => {
    await page.getByRole('button', { name: 'Network' }).click();
    await page.waitForFunction(() => {
      const el = document.getElementById('nw_loading');
      return el && el.textContent !== 'Loading…';
    }, { timeout: 10000 });

    const nwText = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.textContent.trim() : '';
    }, 'nw_loading');
    assert.ok(nwText.length > 0, 'network section loaded');
  });

  test('network tab has rename and password controls', async ({ page }) => {
    await page.getByRole('button', { name: 'Network' }).click();
    await page.waitForFunction(() => {
      const el = document.getElementById('nw_loading');
      return el && el.textContent !== 'Loading…';
    }, { timeout: 10000 });
    await page.locator('#nw_new_ssid').waitFor();
    await page.locator('#nw_new_pw').waitFor();
  });

  test('configuration tab loads schema-driven fields', async ({ page }) => {
    await page.getByRole('button', { name: 'Configuration' }).click();
    await waitForText(page, 'cfg_content', 'Loading…', 15000);

    const stepSize = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.value : '';
    }, 'cfg_step_size');
    assert.ok(stepSize.length > 0, 'config: step_size field populated');
  });

  test('configuration tab step_size round-trip', async ({ page }) => {
    await page.getByRole('button', { name: 'Configuration' }).click();
    await waitForText(page, 'cfg_content', 'Loading…', 15000);

    const originalStepSize = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.value : '';
    }, 'cfg_step_size');
    const probeValue = String(parseInt(originalStepSize, 10) + 1024);

    await page.evaluate((args) => {
      const el = document.getElementById(args.id);
      if (el) el.value = args.val;
    }, { id: 'cfg_step_size', val: probeValue });

    await page.evaluate(() => {
      document.querySelector('.cbi-button-save')?.click();
    });

    await page.waitForFunction(() => {
      const el = document.getElementById('cfg_save_state');
      return el && el.textContent.includes('Saved');
    }, { timeout: 10000 });

    await page.getByRole('button', { name: 'Overview' }).click();
    await page.waitForTimeout(500);
    await page.getByRole('button', { name: 'Configuration' }).click();
    await waitForText(page, 'cfg_content', 'Loading…', 15000);

    const reloadedStepSize = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.value : '';
    }, 'cfg_step_size');
    assert.equal(reloadedStepSize, probeValue, 'config round-trip: step_size persisted');

    await page.evaluate((args) => {
      const el = document.getElementById(args.id);
      if (el) el.value = args.val;
    }, { id: 'cfg_step_size', val: originalStepSize });
    await page.evaluate(() => {
      document.querySelector('.cbi-button-save')?.click();
    });
    await page.waitForFunction(() => {
      const el = document.getElementById('cfg_save_state');
      return el && el.textContent.includes('Saved');
    }, { timeout: 10000 });
  });

  test('logs tab loads', async ({ page }) => {
    await page.getByRole('button', { name: 'Logs' }).click();
    await waitForText(page, 'logs_box', 'Loading…', 10000);
    const logs = await page.evaluate((id) => {
      const el = document.getElementById(id);
      return el ? el.textContent.trim() : '';
    }, 'logs_box');
    assert.ok(logs.length > 0, 'logs loaded');
  });

  test('advanced tab loads JSON editors', async ({ page }) => {
    await page.getByRole('button', { name: 'Advanced' }).click();
    await page.waitForFunction(() => {
      const cfg = document.getElementById('config_editor');
      const ids = document.getElementById('identities_editor');
      return cfg && cfg.value && ids && ids.value;
    }, { timeout: 10000 });

    const configEditor = page.locator('#config_editor');
    const configText = await configEditor.inputValue();
    const configJSON = JSON.parse(configText);
    assert.ok(configJSON.metric, 'config editor has metric field');
    assert.ok(configJSON.step_size, 'config editor has step_size field');
  });

  test('advanced tab config.json validation rejects invalid JSON', async ({ page }) => {
    await page.getByRole('button', { name: 'Advanced' }).click();
    await page.waitForFunction(() => {
      const cfg = document.getElementById('config_editor');
      return cfg && cfg.value;
    }, { timeout: 10000 });

    await page.locator('#config_editor').fill('not valid json');
    await page.getByRole('button', { name: 'Validate' }).first().click();

    await page.waitForFunction(() => {
      const el = document.getElementById('config_state');
      return el && el.textContent.includes('Invalid JSON');
    }, { timeout: 5000 });
  });

  test('advanced tab config.json round-trip', async ({ page }) => {
    await page.getByRole('button', { name: 'Advanced' }).click();
    await page.waitForFunction(() => {
      const cfg = document.getElementById('config_editor');
      return cfg && cfg.value;
    }, { timeout: 10000 });

    const configEditor = page.locator('#config_editor');
    const originalConfig = JSON.parse(await configEditor.inputValue());

    const probe = { ...originalConfig, config_version: `${originalConfig.config_version}-pwtest` };
    await configEditor.fill(JSON.stringify(probe, null, 2));
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

    const reloadedConfig = JSON.parse(await configEditor.inputValue());
    assert.equal(reloadedConfig.config_version, probe.config_version, 'config round-trip persisted');

    await configEditor.fill(JSON.stringify(originalConfig, null, 2));
    await page.getByRole('button', { name: 'Save config.json' }).click();
    await page.waitForFunction(() => {
      const el = document.getElementById('config_state');
      return el && el.textContent.includes('Saved');
    }, { timeout: 5000 });
  });

  test('all six tabs are present', async ({ page }) => {
    for (const name of ['Overview', 'Wallet', 'Network', 'Configuration', 'Logs', 'Advanced']) {
      await expect(page.getByRole('button', { name })).toBeVisible();
    }
  });
});
