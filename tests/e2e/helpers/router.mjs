import { execSync } from 'child_process';
import { mkdirSync, existsSync, writeFileSync, readFileSync, unlinkSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';

const HOST = process.env.TOLLGATE_SSH_HOST || '192.168.13.112';
const PASS = process.env.TOLLGATE_LUCI_PASSWORD;
const LOCK_DIR = join(tmpdir(), 'cashu-lock');

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

export function fundViaCLI(token) {
	const escaped = token.replace(/'/g, "'\\''");
	const out = ssh(`tollgate --json wallet fund '${escaped}'`);
	return JSON.parse(out);
}

function withLock(fn) {
	if (!existsSync(LOCK_DIR)) mkdirSync(LOCK_DIR, { recursive: true });
	const lockFile = join(LOCK_DIR, 'mint.lock');
	let acquired = false;
	for (let i = 0; i < 60; i++) {
		try {
			writeFileSync(lockFile, process.pid.toString(), { flag: 'wx' });
			acquired = true;
			break;
		} catch {
			const age = Date.now() - (parseInt(readFileSync(lockFile, 'utf8').split('\n')[1] || '0', 10) || 0);
			if (age > 30000) { try { unlinkSync(lockFile); } catch {} }
			execSync('sleep 1', { timeout: 2000 });
		}
	}
	if (!acquired) {
		try { unlinkSync(lockFile); } catch {}
		writeFileSync(lockFile, process.pid.toString(), { flag: 'wx' });
	}
	writeFileSync(lockFile, process.pid.toString() + '\n' + Date.now());
	try {
		return fn();
	} finally {
		try { unlinkSync(lockFile); } catch {}
	}
}

export function mintTestnutTokens(amountSats) {
	return withLock(() => {
		const balOut = execSync('cashu -h https://testnut.cashu.exchange balance', { encoding: 'utf8', timeout: 15000 });
		const currentBalance = parseInt(balOut.match(/Balance: (\d+)/)?.[1] || '0', 10);
		if (currentBalance < amountSats + 10) {
			execSync(
				`cashu -h https://testnut.cashu.exchange -y invoice ${amountSats + 50}`,
				{ encoding: 'utf8', timeout: 60000 }
			);
		}
		const out = execSync(
			`cashu -h https://testnut.cashu.exchange -y send ${amountSats}`,
			{ encoding: 'utf8', timeout: 30000 }
		);
		const lines = out.split('\n');
		const tokenLine = lines.find(l => l.startsWith('cashuA') || l.startsWith('cashuB'));
		if (!tokenLine) throw new Error('No token in cashu output: ' + out);
		return tokenLine.trim();
	});
}
