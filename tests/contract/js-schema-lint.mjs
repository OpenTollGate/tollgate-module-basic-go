import { readFileSync } from 'node:fs';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));
const root = resolve(__dirname, '..', '..');

const schemaPath = resolve(root, 'src/config_manager/config_schema.go');

const schemaSrc = readFileSync(schemaPath, 'utf8');

const schemaKeyRe = /JSONKey:\s*"([^"]+)"/g;
const schemaKeys = new Set();
let m;
while ((m = schemaKeyRe.exec(schemaSrc)) !== null) {
	schemaKeys.add(m[1]);
}

if (schemaKeys.size === 0) {
	console.error('FAIL: No JSONKey entries found in schema source');
	process.exit(1);
}

const structSrc = readFileSync(resolve(root, 'src/config_manager/config_manager_config.go'), 'utf8');
const jsonTagRe = /json:"([^,"]+)/g;
const structTags = new Set();
while ((m = jsonTagRe.exec(structSrc)) !== null) {
	structTags.add(m[1]);
}

let failed = false;

for (const tag of structTags) {
	if (tag === '' || tag === '-') continue;
	if (schemaKeys.has(tag)) continue;

	const hasSchemaParent = [...schemaKeys].some(k => tag.startsWith(k + '.') || tag.startsWith(k + '_'));
	if (hasSchemaParent) continue;

	console.error(`FAIL: Config struct has json tag "${tag}" but no matching schema entry`);
	failed = true;
}

if (failed) {
	console.error(`Schema has ${schemaKeys.size} entries, struct has ${structTags.size} json tags`);
	process.exit(1);
}

console.log(`PASS: Schema consistency check (${schemaKeys.size} schema entries cover all struct tags)`);
