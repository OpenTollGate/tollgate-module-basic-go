#!/usr/bin/env node
/**
 * TollGate LuCI UI Round-Trip E2E Test
 *
 * Tests that modifying SAFE (trivial, non-destructive) config fields via the
 * LuCI UI correctly persists values through a save → reload cycle.
 *
 * SAFETY:
 *   - Snapshots the FULL config before the test
 *   - Only modifies trivial fields (config_version, probe_retry_count,
 *     discovery_timeout_s, millisecond_renewal_offset, bytes_renewal_offset)
 *   - NEVER touches: mint URLs, profit_share, identities, wifi password,
 *     SSID, payout addresses, private keys
 *   - Always restores original config on exit (success or failure)
 *
 * Usage:
 *   node tests/e2e/round-trip.mjs
 *
 * Prerequisites:
 *   - npm install playwright (or npx playwright install chromium)
 *   - Router accessible at ROUTER_URL (default http://192.168.13.202:8080)
 *   - Login credentials in ROUTER_USER / ROUTER_PASS env vars
 */

import { chromium } from 'playwright';

// ─── Configuration ────────────────────────────────────────────────
const ROUTER_URL = process.env.ROUTER_URL || 'http://192.168.13.202:8080';
const ROUTER_USER = process.env.ROUTER_USER || 'root';
const ROUTER_PASS = process.env.ROUTER_PASS || 'c03rad0r123';
const TOLLGATE_PATH = '/cgi-bin/luci/admin/services/tollgate-payments';
const TIMEOUT = 30000;

// ─── Test field definitions (SAFE fields only) ────────────────────
// Each entry: [selector, type, testValue, readBack]
// type: 'text' | 'number' | 'select' | 'checkbox'
const TEST_FIELDS = [
  // General section — config_version is purely informational
  { selector: '#config_version', type: 'text', testValue: 'e2e-test-v9.9.9', readBack: 'e2e-test-v9.9.9' },

  // Upstream detector — probe_retry_count is a harmless retry counter
  { selector: '#probe_retry_count', type: 'number', testValue: '7', readBack: '7' },

  // Upstream detector — discovery_timeout is a harmless timeout
  { selector: '#discovery_timeout_s', type: 'number', testValue: '600', readBack: '600' },

  // Upstream session manager — millisecond_renewal_offset is harmless
  { selector: '#millisecond_renewal_offset', type: 'number', testValue: '999', readBack: '999' },

  // Upstream session manager — bytes_renewal_offset is harmless
  { selector: '#bytes_renewal_offset', type: 'number', testValue: '888', readBack: '888' },
];

// ─── Helpers ──────────────────────────────────────────────────────
let originalConfig = null;
let page = null;
let context = null;
let browser = null;

async function apiCall(action, body = null) {
  const url = `${ROUTER_URL}/cgi-bin/tollgate-api?action=${action}`;
  const opts = { method: body ? 'POST' : 'GET' };
  if (body) {
    opts.headers = { 'Content-Type': 'application/json' };
    opts.body = typeof body === 'string' ? body : JSON.stringify(body);
  }
  const res = await fetch(url, opts);
  return res.json();
}

async function snapshotConfig() {
  const res = await apiCall('read');
  if (!res.ok) throw new Error(`Failed to snapshot config: ${JSON.stringify(res)}`);
  return res.config;
}

async function restoreConfig(cfg) {
  console.log('  Restoring original config...');
  const res = await apiCall('save', JSON.stringify(cfg));
  if (!res.ok) {
    console.error('  ⚠️  RESTORE FAILED! Manual restore needed from backup.');
    console.error('  Config to restore:', JSON.stringify(cfg).slice(0, 200) + '...');
  } else {
    console.log('  ✅ Config restored successfully');
  }
}

async function login(page) {
  console.log('  Logging in...');
  await page.goto(`${ROUTER_URL}${TOLLGATE_PATH}`, {
    waitUntil: 'networkidle',
    timeout: TIMEOUT,
  });
  await page.waitForTimeout(1000);

  await page.fill('input[name="luci_username"]', ROUTER_USER);
  await page.fill('input[name="luci_password"]', ROUTER_PASS);
  await page.evaluate(() => document.querySelector('form').submit());
  await page.waitForTimeout(4000);
  await page.waitForLoadState('networkidle').catch(() => {});

  // Verify login succeeded
  const body = await page.evaluate(() => document.body?.innerText?.slice(0, 200));
  if (body.includes('Invalid username')) {
    throw new Error('Login failed: Invalid credentials');
  }
  console.log('  ✅ Logged in');
}

async function navigateToTollGate(page) {
  await page.goto(`${ROUTER_URL}${TOLLGATE_PATH}`, {
    waitUntil: 'networkidle',
    timeout: TIMEOUT,
  });
  await page.waitForTimeout(3000);

  // Verify the TollGate page loaded
  const title = await page.evaluate(() => {
    const h2 = document.querySelector('h2[name="content"]');
    return h2 ? h2.textContent : null;
  });
  if (title !== 'TollGate') {
    throw new Error(`TollGate page did not load. Got title: ${title}`);
  }
}

async function clickConfigTab(page) {
  await page.click('#tab_config');
  await page.waitForTimeout(1500);
}

async function setFieldValue(page, field) {
  if (field.type === 'text') {
    await page.fill(field.selector, field.testValue);
  } else if (field.type === 'number') {
    await page.fill(field.selector, '');
    await page.fill(field.selector, field.testValue);
  } else if (field.type === 'select') {
    await page.selectOption(field.selector, field.testValue);
  } else if (field.type === 'checkbox') {
    const cb = await page.$(field.selector);
    const isChecked = await cb.isChecked();
    if (isChecked !== field.testValue) {
      await cb.click();
    }
  }
}

async function getFieldValue(page, field) {
  if (field.type === 'text') {
    return await page.inputValue(field.selector);
  } else if (field.type === 'number') {
    return await page.inputValue(field.selector);
  } else if (field.type === 'select') {
    return await page.inputValue(field.selector);
  } else if (field.type === 'checkbox') {
    return await page.isChecked(field.selector);
  }
}

async function saveConfig(page) {
  await page.click('#save_form');
  await page.waitForTimeout(3000);

  // Check the validation_box for "Saved." message
  const statusText = await page.evaluate(() => {
    const box = document.querySelector('#validation_box');
    return box ? box.textContent : '';
  });

  if (!statusText.includes('Saved')) {
    throw new Error(`Save failed. Status: ${statusText}`);
  }
  return statusText;
}

// ─── Test Runner ──────────────────────────────────────────────────
async function runTests() {
  const results = { passed: 0, failed: 0, errors: [] };

  try {
    // ── Phase 0: Snapshot config ──
    console.log('\n📦 Phase 0: Snapshotting current config...');
    originalConfig = await snapshotConfig();
    console.log(`  ✅ Config snapshot saved (keys: ${Object.keys(originalConfig).join(', ')})`);

    // ── Phase 1: Launch browser ──
    console.log('\n🚀 Phase 1: Launching browser...');
    browser = await chromium.launch({ headless: true });
    context = await browser.newContext({ viewport: { width: 1400, height: 900 } });
    page = await context.newPage();

    const consoleErrors = [];
    page.on('console', msg => {
      if (msg.type() === 'error') consoleErrors.push(msg.text());
    });
    page.on('pageerror', err => consoleErrors.push(`PAGE ERROR: ${err.message}`));

    // ── Phase 2: Login ──
    console.log('\n🔐 Phase 2: Authentication...');
    await login(page);

    // ── Phase 3: Navigate to TollGate ──
    console.log('\n📋 Phase 3: Navigate to TollGate settings...');
    await navigateToTollGate(page);
    await page.screenshot({ path: '/tmp/tollgate-e2e/01-initial-load.png', fullPage: true });
    console.log('  ✅ TollGate page loaded');

    // ── Phase 4: Verify Dashboard tab renders ──
    console.log('\n📊 Phase 4: Verify Dashboard tab...');
    // Dashboard is the default active tab
    const dashboardContent = await page.evaluate(() => {
      const pane = document.querySelector('.tg-pane.active');
      return pane ? pane.innerText.slice(0, 500) : 'NO PANE';
    });

    if (!dashboardContent.includes('Service status')) {
      throw new Error(`Dashboard tab did not render correctly. Content: ${dashboardContent.slice(0, 200)}`);
    }
    console.log('  ✅ Dashboard tab renders correctly');

    // ── Phase 5: Verify all tabs render ──
    console.log('\n📑 Phase 5: Verify all tabs render...');
    for (const tab of ['tab_network', 'tab_config', 'tab_identities', 'tab_dashboard']) {
      await page.click(`#${tab}`);
      await page.waitForTimeout(1500);
      const paneExists = await page.evaluate(() => {
        const pane = document.querySelector('.tg-pane.active');
        return pane !== null && pane.innerText.length > 10;
      });
      if (!paneExists) {
        throw new Error(`Tab ${tab} did not render an active pane`);
      }
      const tabName = tab.replace('tab_', '');
      console.log(`  ✅ ${tabName} tab renders`);
    }
    await page.screenshot({ path: '/tmp/tollgate-e2e/02-all-tabs.png', fullPage: true });

    // ── Phase 6: Record pre-test field values ──
    console.log('\n📝 Phase 6: Record pre-test field values...');
    await clickConfigTab(page);

    const preTestValues = {};
    for (const field of TEST_FIELDS) {
      preTestValues[field.selector] = await getFieldValue(page, field);
      console.log(`  ${field.selector}: "${preTestValues[field.selector]}"`);
    }

    // ── Phase 7: Set test values and save ──
    console.log('\n✏️  Phase 7: Set test values and save...');
    for (const field of TEST_FIELDS) {
      await setFieldValue(page, field);
      console.log(`  ${field.selector} → "${field.testValue}"`);
    }

    const saveStatus = await saveConfig(page);
    console.log(`  ✅ Save succeeded: ${saveStatus.split('\n')[0]}`);
    await page.screenshot({ path: '/tmp/tollgate-e2e/03-after-save.png', fullPage: true });

    // ── Phase 8: Reload and verify persistence ──
    console.log('\n🔄 Phase 8: Reload page and verify persistence...');
    await navigateToTollGate(page);
    await clickConfigTab(page);
    await page.waitForTimeout(2000);

    for (const field of TEST_FIELDS) {
      const actualValue = await getFieldValue(page, field);
      const passed = actualValue === field.readBack;
      if (passed) {
        console.log(`  ✅ ${field.selector}: "${actualValue}" === "${field.readBack}"`);
        results.passed++;
      } else {
        console.log(`  ❌ ${field.selector}: "${actualValue}" !== "${field.readBack}" (expected)`);
        results.failed++;
        results.errors.push(`${field.selector}: expected "${field.readBack}", got "${actualValue}"`);
      }
    }

    await page.screenshot({ path: '/tmp/tollgate-e2e/04-after-reload.png', fullPage: true });

    // ── Phase 9: Verify console errors ──
    console.log('\n🐛 Phase 9: Check for console errors...');
    // Filter out expected 403 from the login redirect
    const realErrors = consoleErrors.filter(e =>
      !e.includes('403') && !e.includes('404') && !e.includes('Failed to load resource')
    );
    if (realErrors.length > 0) {
      console.log(`  ⚠️  ${realErrors.length} unexpected console errors:`);
      realErrors.forEach(e => console.log(`    ${e}`));
    } else {
      console.log('  ✅ No unexpected console errors');
    }

    // ── Phase 10: Restore original config ──
    console.log('\n♻️  Phase 10: Restore original config...');
    await restoreConfig(originalConfig);

    // ── Phase 11: Verify restore worked ──
    console.log('\n🔍 Phase 11: Verify restore...');
    await navigateToTollGate(page);
    await clickConfigTab(page);
    await page.waitForTimeout(2000);

    let restoreOk = true;
    for (const field of TEST_FIELDS) {
      const restoredValue = await getFieldValue(page, field);
      const expected = preTestValues[field.selector];
      if (restoredValue !== expected) {
        console.log(`  ❌ ${field.selector}: "${restoredValue}" !== "${expected}" (original)`);
        restoreOk = false;
      } else {
        console.log(`  ✅ ${field.selector}: "${restoredValue}" === "${expected}" (original)`);
      }
    }

    if (!restoreOk) {
      results.errors.push('Config restore verification failed — values don\'t match originals');
    }

    await page.screenshot({ path: '/tmp/tollgate-e2e/05-after-restore.png', fullPage: true });

  } catch (err) {
    console.error(`\n💥 FATAL ERROR: ${err.message}`);
    results.errors.push(`FATAL: ${err.message}`);
    results.failed++;
  } finally {
    // Always restore config if we have a snapshot
    if (originalConfig) {
      try {
        // Double-check: re-snapshot and compare
        const currentCfg = await snapshotConfig();
        const originalJson = JSON.stringify(originalConfig);
        const currentJson = JSON.stringify(currentCfg);
        if (originalJson !== currentJson) {
          console.log('\n⚠️  Config differs from original — forcing restore...');
          await restoreConfig(originalConfig);
        }
      } catch (e) {
        console.error('  ⚠️  Could not verify/restore config:', e.message);
      }
    }

    if (browser) {
      await browser.close();
    }
  }

  // ── Report ──
  console.log('\n' + '═'.repeat(60));
  console.log('  ROUND-TRIP TEST RESULTS');
  console.log('═'.repeat(60));
  console.log(`  Assertions passed: ${results.passed}`);
  console.log(`  Assertions failed: ${results.failed}`);
  if (results.errors.length > 0) {
    console.log('\n  Errors:');
    results.errors.forEach(e => console.log(`    ❌ ${e}`));
  }
  console.log('═'.repeat(60));

  if (results.failed > 0) {
    process.exit(1);
  } else {
    console.log('\n  ✅ ALL TESTS PASSED\n');
    process.exit(0);
  }
}

// Ensure screenshot directory exists
import { mkdirSync } from 'fs';
mkdirSync('/tmp/tollgate-e2e', { recursive: true });

runTests().catch(err => {
  console.error('Unhandled error:', err);
  process.exit(2);
});
