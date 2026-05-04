import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: '*.spec.mjs',
	retries: 1,
	timeout: 60000,
	reporter: [
		['html', { outputFolder: 'report', open: 'never' }],
		['list'],
	],
	use: {
		baseURL: process.env.TOLLGATE_LUCI_URL ?? 'http://192.168.13.112:8080',
		screenshot: 'on',
		trace: 'on-first-retry',
		actionTimeout: 10000,
		storageState: { cookies: [], origins: [] },
	},
	projects: [
		{
			name: 'desktop',
			use: { viewport: { width: 1280, height: 900 } },
		},
		{
			name: 'mobile',
			use: { viewport: { width: 375, height: 812 } },
		},
	],
});
