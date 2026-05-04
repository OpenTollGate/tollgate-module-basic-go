import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = resolve(__dirname, '..', '..');

const settingsPath = resolve(root, 'files/www/luci-static/resources/view/tollgate-payments/settings.js');
const aclPath = resolve(root, 'files/usr/share/rpcd/acl.d/luci-app-tollgate-payments.json');

const settingsSrc = readFileSync(settingsPath, 'utf8');
const acl = JSON.parse(readFileSync(aclPath, 'utf8'));

const cliVarMatch = settingsSrc.match(/var\s+CLI\s*=\s*['"]([^'"]+)['"]/);
const cliVarValue = cliVarMatch ? cliVarMatch[1] : null;

const aclFilePerms = {};
const aclObj = Object.values(acl)[0];
for (const scope of ['read', 'write']) {
	if (aclObj[scope] && aclObj[scope].file) {
		Object.assign(aclFilePerms, aclObj[scope].file);
	}
}

const execPathRe = /fs\.exec_direct\s*\(\s*['"]([^'"]+)['"]/g;
const execPaths = new Set();
let m;
while ((m = execPathRe.exec(settingsSrc)) !== null) {
	execPaths.add(m[1]);
}

const cliUsageRe = /fs\.exec_direct\s*\(\s*CLI\b/g;
let hasCliUsage = cliUsageRe.test(settingsSrc);
if (hasCliUsage && cliVarValue) {
	execPaths.add(cliVarValue);
}

let failed = false;

for (const path of execPaths) {
	const perms = aclFilePerms[path];
	if (!perms) {
		console.error(`FAIL: settings.js executes ${path} but ACL has no entry`);
		failed = true;
		continue;
	}
	if (!perms.includes('exec')) {
		console.error(`FAIL: settings.js executes ${path} but ACL only grants [${perms.join(', ')}]`);
		failed = true;
	}
}

const expectedBins = [
	'/usr/bin/tollgate',
	'/sbin/logread',
	'/etc/init.d/tollgate-wrt',
	'/etc/init.d/nodogsplash',
];
for (const bin of expectedBins) {
	if (!execPaths.has(bin)) {
		console.error(`WARN: Expected bin ${bin} not found in settings.js exec calls`);
	}
}

const aclExecPaths = Object.keys(aclFilePerms).filter(p => aclFilePerms[p].includes('exec'));
for (const aclPath_entry of aclExecPaths) {
	if (!execPaths.has(aclPath_entry)) {
		console.error(`WARN: ACL grants exec on ${aclPath_entry} but settings.js never calls it`);
	}
}

if (failed) {
	process.exit(1);
}

const uniqueCalls = [...execPaths].sort();
console.log(`PASS: All ${uniqueCalls.length} exec paths in settings.js are covered by ACL`);
for (const p of uniqueCalls) {
	console.log(`  ${p}`);
}
