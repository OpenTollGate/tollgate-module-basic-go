import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = resolve(__dirname, '..', '..');

const settingsPath = resolve(root, 'files/www/luci-static/resources/view/tollgate-payments/settings.js');
const schemaPath = resolve(root, 'src/config_manager/config_schema.go');

const settingsSrc = readFileSync(settingsPath, 'utf8');
const schemaSrc = readFileSync(schemaPath, 'utf8');

const schemaKeyRe = /JSONKey:\s*"([^"]+)"/g;
const schemaKeys = new Set();
let m;
while ((m = schemaKeyRe.exec(schemaSrc)) !== null) {
	schemaKeys.add(m[1]);
}

const hardcodedListRe = /(?:simpleFields|mintFields|scalarFields)\s*=\s*\[/;
if (hardcodedListRe.test(settingsSrc)) {
	console.error('FAIL: settings.js contains a hardcoded field-name array (simpleFields/mintFields/scalarFields). saveAllConfig should iterate the schema instead.');
	process.exit(1);
}
console.log('PASS: No hardcoded field-name arrays in settings.js');

const uiOnlyIds = new Set([
	'content', 'save_state', 'advanced_raw',
	'mints_body', 'ps_body', 'pi_body'
]);

const rowPrefixes = ['mint_', 'ps_', 'pi_'];

const literalKeyRe = /['"]cfg_([a-z][a-z0-9_]*)['"]/g;
const literalKeys = new Set();
while ((m = literalKeyRe.exec(settingsSrc)) !== null) {
	const key = m[1];
	if (uiOnlyIds.has(key)) continue;

	let isRowId = false;
	for (const prefix of rowPrefixes) {
		if (key.startsWith(prefix)) {
			const rest = key.slice(prefix.length);
			if (/^\d+_/.test(rest)) {
				isRowId = true;
				break;
			}
		}
	}
	if (isRowId) continue;

	if (key === 'mint_' || key === 'ps_' || key === 'pi_') continue;

	literalKeys.add(key);
}

let failed = false;
for (const key of literalKeys) {
	if (schemaKeys.has(key)) continue;
	console.error('FAIL: settings.js has literal "cfg_' + key + '" but schema has no json_key "' + key + '"');
	failed = true;
}

if (failed) {
	process.exit(1);
}

const jsFieldKeys = [...literalKeys].filter(function(k) { return schemaKeys.has(k); });
console.log('PASS: All literal cfg_* keys in settings.js match schema entries');
console.log('  Schema json_keys: ' + schemaKeys.size);
console.log('  Literal cfg_* field keys: ' + jsFieldKeys.length);
for (const k of jsFieldKeys.sort()) {
	console.log('  cfg_' + k);
}
