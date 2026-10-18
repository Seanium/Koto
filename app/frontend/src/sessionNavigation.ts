import type { Binding } from './api/types.ts'
import { accountClaims, productKey } from './api/identity.ts'

export function destinationFilters(binding: Binding | undefined, harness: string) {
  const owners = accountClaims(binding)
  return {
    scope: binding?.meta.sourceProfile ? productKey(binding.meta.sourceProfile) : 'all',
    profile: binding?.connector,
    account: owners.length === 1 ? owners[0].id : undefined,
  }
}

/** The list highlight and detail always derive from the same visible selection. */
export function visibleSelection<T extends { id: string }>(items: readonly T[], selected?: string): T | undefined {
  return items.find(item => item.id === selected) ?? items[0]
}
