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

const MINT_URL = 'https://testnut.cashu.exchange';
const CASHU = `echo "" | cashu -h ${MINT_URL}`;

export function mintTestnutTokens(amountSats) {
	return withLock(() => {
		const mintAmount = amountSats + 10;
		const createOut = execSync(`${CASHU} invoice ${mintAmount} --no-check`, { encoding: 'utf8', timeout: 30000, shell: '/bin/bash' });
		const idMatch = createOut.match(/--id ([a-f0-9]+)/);
		if (idMatch) {
			execSync(`${CASHU} invoice ${mintAmount} --id ${idMatch[1]}`, { encoding: 'utf8', timeout: 30000, shell: '/bin/bash' });
		}
		const out = execSync(`${CASHU} send ${amountSats}`, { encoding: 'utf8', timeout: 30000, shell: '/bin/bash' });
		const lines = out.split('\n');
		const tokenLine = lines.find(l => l.startsWith('cashuA') || l.startsWith('cashuB'));
		if (!tokenLine) throw new Error('No token in cashu output: ' + out);
		return tokenLine.trim();
	});
}

export function getPrivateSSID() {
	return ssh("uci get wireless.private_radio0.ssid").trim();
}

export function setPrivateSSID(ssid) {
	ssh(`uci set wireless.private_radio0.ssid='${ssid.replace(/'/g, "'\\''")}'`);
	ssh(`uci set wireless.private_radio1.ssid='${ssid.replace(/'/g, "'\\''")}'`);
	ssh('uci commit wireless && wifi reload');
}

export function isSafeForNetworkTests() {
	const routerIP = HOST;
	try {
		const routeOut = execSync('netstat -rn 2>/dev/null', { encoding: 'utf8', timeout: 5000 });
		const routeLine = routeOut.split('\n').find(l => l.includes(routerIP) && l.includes('UH'));
		if (!routeLine) return false;
		const match = routeLine.match(/\s+(en\d+|eth\d+)\s+/);
		if (!match) return false;
		try { execSync(`ping -c 1 -t 2 ${routerIP}`, { timeout: 5000 }); } catch { return false; }
		return true;
	} catch {
		return false;
	}
}
