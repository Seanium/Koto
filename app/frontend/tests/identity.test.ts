import assert from 'node:assert/strict'
import { accountClaims, storageAccount, matchesIdentity, matchesProduct, preferredCopyTarget, UNKNOWN_ACCOUNT } from '../src/api/identity.ts'
import type { AgentProfile, Binding, IdentityClaim } from '../src/api/types.ts'
import { desktopSourceDescription, fromDesktop, productName } from '../src/api/model.ts'

const claim = (id: string, kind: IdentityClaim['kind'] = 'account'): IdentityClaim => ({ kind, id, scope: 'storage', evidence: 'native sessions.user_id' })
const binding = (connector: string, claims: IdentityClaim[]): Binding => ({ harness: 'workbuddy', connector, session: 'same-native-id', thread: 'thread', leaf: null, path: null, meta: { identity: { status: claims.length ? 'known' : 'unknown', claims, providers: [{ id: 'gateway', scope: 'current-config', evidence: 'settings' }] } } })
const a = binding('workbuddy@a', [claim('owner-a')]), b = binding('workbuddy@b', [claim('owner-b')])
assert.equal(matchesIdentity([a, b], 'workbuddy@a', 'owner-b'), false, 'profile and owner must match the same binding')
assert.equal(matchesIdentity([a, b], 'workbuddy@b', 'owner-b'), true)
assert.equal(matchesIdentity([binding('pi', [claim('gateway', 'api-configuration')])], 'pi', UNKNOWN_ACCOUNT), true, 'API configuration does not prove an account')
assert.deepEqual(accountClaims(binding('codex', [claim('org', 'organization')])), [])
const profiles: AgentProfile[] = [{ id: 'workbuddy@a', harness: 'workbuddy', variant: 'desktop', default: false, root: '/a', label: 'a', kind: 'custom', writable: true, enabled: true, accountSelectable: true, accounts: [claim('owner-a'), claim('owner-b')], targetAccounts: [claim('owner-a'), claim('owner-b')] }, { id: 'workbuddy-ai', harness: 'workbuddy-ai', variant: 'desktop', default: true, root: '/intl', label: 'intl', kind: 'default', writable: true, enabled: true, accountSelectable: true, accounts: [claim('owner-a')], targetAccounts: [claim('owner-a')] }]
assert.deepEqual(preferredCopyTarget(profiles, { profile: 'workbuddy@a', harness: 'workbuddy', account: 'owner-b' }), { profile: 'workbuddy@a', account: 'owner-b' })
assert.deepEqual(preferredCopyTarget(profiles, { profile: 'workbuddy@a', harness: 'workbuddy', account: 'missing' }), { profile: 'workbuddy@a', account: undefined }, 'multiple owners are never guessed')
assert.deepEqual(preferredCopyTarget([{ ...profiles[0], accountSelectable: false }], { profile: 'workbuddy@a', harness: 'workbuddy', account: 'owner-a' }), { profile: 'workbuddy@a', account: undefined }, 'historical ownership does not imply target account control')
assert.deepEqual(preferredCopyTarget([]), { profile: '' })
const cursorCLI = { ...profiles[0], id: 'cursor@cli-root', harness: 'cursor', variant: 'cli', default: true, accountSelectable: false }
const cursorApp = { ...cursorCLI, id: 'cursor@app-root', variant: 'ide' }
assert.equal(preferredCopyTarget([cursorCLI, cursorApp], { profile: cursorApp.id, harness: 'cursor', variant: 'ide' }).profile, cursorApp.id, 'same harness cannot move an App source to the CLI')
assert.equal(preferredCopyTarget([cursorCLI, cursorApp], { harness: 'cursor', variant: 'ide' }).profile, cursorApp.id, 'variant default does not depend on a short profile ID')
assert.deepEqual(preferredCopyTarget([{ ...cursorApp, default: false }, { ...cursorApp, id: 'cursor@second-app', default: false }]), { profile: '' }, 'two custom directories require an explicit choice')
const appBinding = { ...a, harness: 'cursor', connector: cursorApp.id, meta: { sourceProfile: cursorApp } }
assert.equal(matchesProduct([appBinding], 'cursor/ide'), true)
assert.equal(matchesProduct([appBinding], 'cursor/cli'), false)
console.log('Identity: profile isolation, unknown evidence and explicit owner defaults passed')

const creator = binding('workbuddy@a', [{...claim('owner-a'),scope:'creator'}])
assert.equal(storageAccount(creator), undefined)
assert.deepEqual(preferredCopyTarget(profiles, {profile:'workbuddy@a',harness:'workbuddy',account:storageAccount(creator)}), {profile:'workbuddy@a',account:undefined}, 'a creator claim cannot choose a storage owner')
assert.equal(storageAccount(binding('workbuddy@a',[claim('owner-a'),claim('owner-b')])),undefined,'multiple storage owners are never guessed')
assert.equal(storageAccount(binding('workbuddy@a',[{...claim('owner-a'),scope:'creator'},claim('owner-b')])), 'owner-b')
assert.equal(storageAccount(binding('workbuddy@a',[claim('owner-a'),{...claim('owner-a'),scope:'session'}])), 'owner-a')

const desktopCode: AgentProfile = { ...cursorApp, id: 'claude-desktop-code', harness: 'claude-code', variant: 'desktop', kind: 'desktop-account' }
const cowork: AgentProfile = { ...desktopCode, id: 'claude-cowork-task', kind: 'cowork', writable: false, default: false }
const coworkBinding: Binding = { ...a, harness: 'claude-code', connector: cowork.id, meta: { frontend: 'claude-desktop', desktopMode: 'cowork', sourceProfile: cowork } }
assert.equal(productName(cowork.harness, cowork.variant), 'Claude Desktop')
assert.equal(matchesProduct([coworkBinding], 'claude-code/desktop'), true)
assert.equal(matchesProduct([coworkBinding], 'claude-code/cli'), false)
assert.equal(fromDesktop(coworkBinding), true)
assert.match(desktopSourceDescription([coworkBinding]), /Cowork/)
assert.equal(preferredCopyTarget([cowork, desktopCode], { profile: cowork.id, harness: cowork.harness, variant: cowork.variant }).profile, desktopCode.id, 'readable Cowork source must not become a destination')
assert.deepEqual(preferredCopyTarget([cowork]), { profile: '' }, 'Cowork-only profile has no writable destination')
