import { execSync } from 'child_process';

const HOST = process.env.TOLLGATE_SSH_HOST || '192.168.13.112';
const PASS = process.env.TOLLGATE_LUCI_PASSWORD;

function ssh(cmd) {
	return execSync(
		`sshpass -e ssh -o StrictHostKeyChecking=no -o ConnectTimeout=5 root@${HOST} '${cmd.replace(/'/g, "'\\''")}'`,
		{ encoding: 'utf8', env: { ...process.env, SSHPASS: PASS }, timeout: 15000 }
	).trim();
}

export function fileExists(path) {
	try { ssh(`test -f '${path}'`); return true; } catch { return false; }
}

export function readFile(path) {
	return ssh(`cat '${path}'`);
}

export function cleanupFiles(pattern) {
	ssh(`rm -f ${pattern}`);
}

export function getWalletBalance() {
	const out = ssh('tollgate --json wallet balance');
	const data = JSON.parse(out);
	return data?.data?.balance_sats ?? 0;
}

export function getWalletInfo() {
	const out = ssh('tollgate --json wallet info');
	return JSON.parse(out);
}

export function drainViaCLI() {
	const out = ssh("tollgate --json wallet drain cashu");
	return JSON.parse(out);
}
