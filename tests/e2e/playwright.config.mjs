import { defineConfig } from '@playwright/test';

const viewport = process.env.TOLLGATE_VIEWPORT || 'desktop';
const viewports = {
	desktop: { width: 1280, height: 900 },
	mobile: { width: 375, height: 812 },
};

export default defineConfig({
	testDir: '.',
	testMatch: '*.spec.mjs',
	retries: 1,
	timeout: 60000,
	workers: 1,
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
			name: viewport,
			use: { viewport: viewports[viewport] || viewports.desktop },
		},
	],
});
