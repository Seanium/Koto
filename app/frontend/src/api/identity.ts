import { tr } from '../i18n/core.ts'
import type { AgentProfile, Binding, IdentityClaim } from './types.ts'

export const UNKNOWN_ACCOUNT = '__unknown__'

/** A provider, model or directory name cannot establish an account identity. */
export function accountClaims(binding?: Binding): IdentityClaim[] {
  if (binding?.meta.identity?.status !== 'known') return []
  const unique = new Map<string, IdentityClaim>()
  for (const claim of binding.meta.identity.claims ?? []) {
    if (claim.kind === 'account' && claim.id && claim.evidence) unique.set(claim.id, claim)
  }
  return [...unique.values()]
}

/** A creator claim identifies who started a session, not its current storage owner. */
export function storageAccount(binding?: Binding): string | undefined {
  if (binding?.meta.identity?.status !== 'known') return undefined
  const ids = new Set((binding.meta.identity.claims ?? []).filter(claim => claim.kind === 'account' && claim.id && claim.evidence && (claim.scope === 'storage' || claim.scope === 'session')).map(claim => claim.id))
  return ids.size === 1 ? [...ids][0] : undefined
}

export const accountName = (claim: IdentityClaim) => claim.label?.trim() || claim.id

export function accountLabel(binding?: Binding): string {
  const claims = accountClaims(binding)
  if (!claims.length) return tr('账号未知')
  if (claims.length > 1) return tr('多个账号')
  return claims[0].scope === 'creator' ? tr('创建账号：{0}', [accountName(claims[0])]) : accountName(claims[0])
}

export function matchesIdentity(bindings: Binding[], profile?: string, account?: string): boolean {
  return bindings.some(binding => {
    if (profile && binding.connector !== profile) return false
    const claims = accountClaims(binding)
    return !account || (account === UNKNOWN_ACCOUNT ? !claims.length : claims.some(claim => claim.id === account))
  })
}

/** Reuse the same native profile and owner only when that target can prove both. */
export const productKey = (profile: { harness: string; variant: string }) => `${profile.harness}/${profile.variant}`
export const matchesProduct = (bindings: Binding[], scope: string) => scope === 'all' || bindings.some(binding =>
  binding.harness === scope || (!!binding.meta.sourceProfile && productKey(binding.meta.sourceProfile) === scope))

export function preferredCopyTarget(profiles: AgentProfile[], source?: { profile?: string; harness?: string; variant?: string; account?: string }): { profile: string; account?: string } {
  const writable = profiles.filter(profile => profile.writable && profile.enabled)
  const same = writable.find(profile => profile.id === source?.profile && profile.harness === source.harness)
  const sourceProduct = writable.filter(profile => profile.harness === source?.harness && profile.variant === source.variant)
  const product = sourceProduct.length ? sourceProduct : writable.filter(profile => writable[0] && productKey(profile) === productKey(writable[0]))
  const target = same ?? product.find(profile => profile.default) ?? (product.length === 1 ? product[0] : undefined)
  if (!target) return { profile: '' }
  const accounts = target.targetAccounts.filter(claim => claim.kind === 'account')
  const account = target.accountSelectable
    ? same && source?.account && accounts.some(claim => claim.id === source.account) ? source.account : accounts.length === 1 ? accounts[0].id : undefined
    : undefined
  return { profile: target.id, account }
}
