import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './e2e',
  timeout: 60000,
  retries: 0,
  use: {
    headless: true,
    baseURL: process.env.TOLLGATE_LUCI_URL ?? `http://${process.env.TOLLGATE_ROUTER ?? '192.168.13.202:8080'}`,
  },
});
