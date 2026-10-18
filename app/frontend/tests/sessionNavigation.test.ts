import assert from 'node:assert/strict'
import { destinationFilters, visibleSelection } from '../src/sessionNavigation.ts'
import type { Binding } from '../src/api/types.ts'
const b = { connector: 'cursor@profile-ide', harness: 'cursor', meta: { sourceProfile: { harness: 'cursor', variant: 'ide' }, identity: { status: 'known', claims: [{ kind: 'account', id: 'owner', scope: 'storage', evidence: 'native' }] } } } as Binding
assert.deepEqual(destinationFilters(b, 'cursor'), { scope: 'cursor/ide', profile: 'cursor@profile-ide', account: 'owner' })
const items = [{id:'a'}, {id:'b'}]
assert.equal(visibleSelection(items, 'gone')?.id, 'a')
assert.equal(visibleSelection(items, 'b')?.id, 'b')
assert.equal(visibleSelection([], 'b'), undefined)
console.log('Navigation: flat product scope, precise target profile/owner and one visible selection passed')
