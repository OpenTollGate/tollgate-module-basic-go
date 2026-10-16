// tref.js -- compute the REAL Hedgehog taptree with the SHIPPED library
// (https://supertestnet.github.io/hedgehog-advanced/tapscript.js -- the bundle
//  index.html:14 loads). This is the oracle xcheck.py compares our Python to.
const { Tap, Address } = require('/tmp/tapscript_mod.js');

const Alice = "f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9";
const Bob   = "5cbdf0646e5db4eaa398f365f2ea7a0e3d419b7e0330e39ce92bddedcac4f9bc";
const KEY   = "ab".repeat(32);
const hexof = x => (typeof x === 'string') ? x : Buffer.from(x).toString('hex');
const enc = s => hexof(Tap.encodeScript(s));

// index.html:89,90,92 -- makeAlicesRevocationScript(chan_id)
const ms = enc([ Alice, "OP_CHECKSIG", Bob, "OP_CHECKSIGADD", 2, "OP_EQUAL" ]);
const rv = enc([ "OP_RIPEMD160", "aa".repeat(20), "OP_EQUALVERIFY", Bob, "OP_CHECKSIG" ]);
const to = enc([ 10, "OP_CHECKSEQUENCEVERIFY", "OP_DROP", Bob, "OP_CHECKSIG" ]);

const tree = [ms, rv, to];
const [outkey] = Tap.getPubKey(KEY, { tree });
const msLeafHex = Tap.tree.getLeaf(ms);
const [, cbMs] = Tap.getPubKey(KEY, { tree, target: ms });

process.stdout.write(JSON.stringify({
  alice: Alice, bob: Bob,
  scripts: { ms, rv, to },
  leafhashes: { ms: msLeafHex, rv: Tap.tree.getLeaf(rv), to: Tap.tree.getLeaf(to) },
  root: hexof(Tap.tree.getRoot(tree)),
  outkey: hexof(outkey),
  cblock_ms: hexof(cbMs),
  address: Address.p2tr.fromPubKey(hexof(outkey), "regtest"),
}) + "\n");
