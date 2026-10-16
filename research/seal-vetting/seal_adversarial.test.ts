// Adversarial check #3: can a party with NO mint and NO counterparty fabricate a
// seal that passes sealChainProblem? If yes, a router treating '' as "access
// granted" is trusting the customer as the issuer.
//
// Run inside the lnurl-wallet repo (has vitest + the real modules).

import {describe, expect, it} from 'vitest'
import {sha256} from '@noble/hashes/sha2.js'
import {bytesToHex, concatBytes, utf8ToBytes} from '@noble/hashes/utils.js'

// --- reimplemented EXACTLY from seals.ts:115-143 -------------------------
const DOMAIN_TAG = utf8ToBytes('LNURLcash/seal/state/v0')

const lengthPrefixed = (text: string): Uint8Array => {
  const bytes = utf8ToBytes(text)
  return concatBytes(Uint8Array.of((bytes.length >> 8) & 0xff, bytes.length & 0xff), bytes)
}
const encodeStateIndex = (n: number): Uint8Array =>
  Uint8Array.of((n >>> 24) & 0xff, (n >>> 16) & 0xff, (n >>> 8) & 0xff, n & 0xff)

type SealState = {
  assetId: string
  name: string
  description: string
  stateIndex: number
  ownerPubkeyHex: string
  prevStateHash: string
}

const encodeSealState = (state: SealState): Uint8Array =>
  concatBytes(
    DOMAIN_TAG,
    Uint8Array.from(Buffer.from(state.assetId, 'hex')),
    lengthPrefixed(state.name),
    lengthPrefixed(state.description),
    encodeStateIndex(state.stateIndex),
    Uint8Array.from(Buffer.from(state.ownerPubkeyHex, 'hex')),
    state.prevStateHash
      ? Uint8Array.from(Buffer.from(state.prevStateHash, 'hex'))
      : new Uint8Array(32)
  )

const sealStateHash = (s: SealState): string =>
  bytesToHex(sha256(encodeSealState(s)))

// --- reimplemented EXACTLY from seals.ts:343-373 -------------------------
const isHex32 = (v: unknown): v is string => /^[0-9a-f]{64}$/i.test(String(v ?? ''))

const sealChainProblem = (states: unknown): string => {
  if (!Array.isArray(states) || states.length === 0) return 'No states to verify.'
  const list = states as SealState[]
  const genesis = list[0]!
  if (genesis.stateIndex !== 0) return 'Genesis must be state index 0.'
  if (genesis.prevStateHash) return 'Genesis must have no previous state.'
  if (!isHex32(genesis.assetId)) return 'Genesis has an invalid asset id.'
  if (!genesis.name?.trim()) return 'Genesis has no name.'
  if (!isHex32(genesis.ownerPubkeyHex)) return 'Genesis has an invalid owner pubkey.'
  for (let i = 1; i < list.length; i++) {
    const prev = list[i - 1]!
    const cur = list[i]!
    if (cur.assetId !== genesis.assetId) return `State ${i}: a different asset id.`
    if (cur.name !== genesis.name || cur.description !== genesis.description)
      return `State ${i}: identity changed.`
    if (cur.stateIndex !== prev.stateIndex + 1) return `State ${i}: bad index.`
    if (cur.prevStateHash !== sealStateHash(prev)) return `State ${i}: does not chain.`
    if (!isHex32(cur.ownerPubkeyHex)) return `State ${i}: invalid owner pubkey.`
  }
  return ''
}

// --- the attack: fabricate from nothing -----------------------------------
const rand32 = () => bytesToHex(crypto.getRandomValues(new Uint8Array(32)))

describe('ADVERSARIAL #3: fabricate a seal with no mint, no note, no counterparty', () => {
  it('a wholly invented 5-state history validates as VALID', () => {
    // No mint. No LNURLcash note. No signature from anyone but me.
    const attackerKey = rand32()
    const victimKey = rand32()

    const states: SealState[] = [{
      assetId: rand32(),          // my own invented asset id
      name: 'Dad\'s Kitchen — Monthly Pass',
      description: '1 month unlimited',
      stateIndex: 0,
      ownerPubkeyHex: attackerKey,
      prevStateHash: ''
    }]
    // Chain 4 transitions onto it.
    for (let i = 1; i <= 4; i++) {
      states.push({
        ...states[i - 1]!,
        stateIndex: i,
        // Final owner is the victim — i.e. a pass "owned by" them.
        ownerPubkeyHex: i === 4 ? victimKey : rand32(),
        prevStateHash: sealStateHash(states[i - 1]!)
      })
    }

    const verdict = sealChainProblem(states)
    console.log('  verdict:', JSON.stringify(verdict))
    console.log('  states:', states.length, 'terminal owner:', states[4]!.ownerPubkeyHex.slice(0, 16))

    // '' means VALID. If this passes, the customer can mint themselves a pass.
    expect(verdict).toBe('')
  })

  it('the SAME fabricated history can be stretched to any depth', () => {
    const states: SealState[] = [{
      assetId: rand32(), name: 'Invented', description: '',
      stateIndex: 0, ownerPubkeyHex: rand32(), prevStateHash: ''
    }]
    for (let i = 1; i <= 500; i++) {
      states.push({
        ...states[i - 1]!, stateIndex: i,
        ownerPubkeyHex: rand32(),
        prevStateHash: sealStateHash(states[i - 1]!)
      })
    }
    expect(sealChainProblem(states)).toBe('')
    console.log('  500-state fabricated chain verdict: VALID')
  })
})

// --- #2: prefix splitting --------------------------------------------------
describe('ADVERSARIAL #2: every PREFIX of a consignment is also valid', () => {
  it('one consignment yields N distinct valid terminal states', () => {
    const states: SealState[] = [{
      assetId: rand32(), name: 'Pass', description: '',
      stateIndex: 0, ownerPubkeyHex: rand32(), prevStateHash: ''
    }]
    for (let i = 1; i <= 5; i++) {
      states.push({
        ...states[i - 1]!, stateIndex: i, ownerPubkeyHex: rand32(),
        prevStateHash: sealStateHash(states[i - 1]!)
      })
    }

    const terminals = new Set<string>()
    for (let n = 1; n <= states.length; n++) {
      const prefix = states.slice(0, n)
      expect(sealChainProblem(prefix)).toBe('')   // every prefix is "valid"
      terminals.add(sealStateHash(prefix[prefix.length - 1]!))
    }
    console.log(`  ${states.length} states -> ${terminals.size} distinct valid terminal hashes`)
    // If a router keys replay on the TERMINAL state hash, these are N spends.
    expect(terminals.size).toBe(states.length)
  })
})
