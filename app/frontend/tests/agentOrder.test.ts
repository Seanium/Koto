import assert from 'node:assert/strict'
import { agentOrder, moveAgent, orderAgentProducts } from '../src/ui/agentOrder.ts'
const names: Record<string,string> = { cursor: 'Cursor', codex: 'Codex', 'claude-code': 'Claude Code', pi: 'Pi', antigravity: 'Antigravity CLI' }
const name = (id: string) => names[id] ?? id
const ids = ['pi', 'cursor', 'codex', 'claude-code', 'antigravity']
assert.deepEqual(agentOrder(ids, [], name), ['antigravity', 'claude-code', 'codex', 'cursor', 'pi'])
const stored = ['cursor', 'future-agent', 'pi', 'codex']
assert.deepEqual(agentOrder(ids, stored, name), [...stored, 'antigravity', 'claude-code'], 'new agents append without reordering existing or unknown entries')
const all = agentOrder(ids, stored, name)
const moved = moveAgent(all, 'codex', 'cursor')
assert.deepEqual(moved, ['codex', 'cursor', 'future-agent', 'pi', 'antigravity', 'claude-code'])
assert.deepEqual(moved.filter(id => id !== 'pi'), ['codex', 'cursor', 'future-agent', 'antigravity', 'claude-code'])
assert.equal(agentOrder(ids, moved, name).indexOf('pi'), 3, 'disabling or re-enabling does not remove the saved position')
assert.deepEqual(moveAgent(moved, 'codex', 'pi', true), ['cursor', 'future-agent', 'pi', 'codex', 'antigravity', 'claude-code'])
assert.deepEqual(moveAgent(moved, 'all', 'pi'), moved, 'non-agent actions never reorder')
const products = [{ harness: 'pi', variant: 'cli' }, { harness: 'cursor', variant: 'cli' }, { harness: 'claude-code', variant: 'cli' }, { harness: 'cursor', variant: 'ide' }, { harness: 'claude-code', variant: 'desktop' }]
const productNames: Record<string,string> = {'pi/cli':'Pi','cursor/cli':'Cursor CLI','cursor/ide':'Cursor Desktop','claude-code/cli':'Claude Code','claude-code/desktop':'Claude Desktop'}
const productName = (id: string) => productNames[id] ?? id
assert.deepEqual(orderAgentProducts(products, [], productName).map(p => `${p.harness}/${p.variant}`), ['claude-code/cli','claude-code/desktop','cursor/cli','cursor/ide','pi/cli'])
const savedProducts = ['cursor/cli','cursor/ide','future-agent/new','pi/cli','claude-code/cli','claude-code/desktop','claude-code/cowork']
const independentlyMoved = moveAgent(savedProducts,'cursor/ide','pi/cli',true)
assert.deepEqual(orderAgentProducts(products,independentlyMoved,productName).map(p => `${p.harness}/${p.variant}`),['cursor/cli','pi/cli','cursor/ide','claude-code/cli','claude-code/desktop'],'Desktop moves independently of the CLI and copy uses the same product order')
assert.deepEqual(orderAgentProducts(products.filter(p=>p.variant!=='ide'),independentlyMoved,productName).map(p=>`${p.harness}/${p.variant}`),['cursor/cli','pi/cli','claude-code/cli','claude-code/desktop'])
assert.equal(agentOrder(products.map(p=>`${p.harness}/${p.variant}`),independentlyMoved,productName).indexOf('cursor/ide'),3,'disabled product retains its position')
console.log('Product ordering: alphabetic default, independent Desktop/CLI and disabled/future entries passed')
