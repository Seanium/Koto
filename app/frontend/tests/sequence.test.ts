import assert from 'node:assert/strict'
import { align } from '../src/analysis/sequence.ts'
assert.deepEqual(align(['a','b','c'], ['a','new','b','c']).map(x=>x.kind), ['equal','added','equal','equal'])
assert.deepEqual(align(['a','x','c'], ['a','y','c']).map(x=>x.kind), ['equal','changed','equal'])
assert.deepEqual(align(['a','b','c'], ['a','c']).map(x=>x.kind), ['equal','removed','equal'])
assert.deepEqual(align(['x','x','y'], ['x','y']).map(x=>x.kind), ['equal','removed','equal'])
const large=Array.from({length:15000},(_,i)=>String(i))
assert.equal(align(large,[...large.slice(0,5000),'new',...large.slice(5000)]).filter(x=>x.kind!=='equal').length,1)
for(let n=0;n<200;n++) {
 const a=Array.from({length:n%13},(_,i)=>String((i*7+n)%5)),b=Array.from({length:n%17},(_,i)=>String((i*3+n)%5))
 const rows=align(a,b)
 assert.deepEqual(rows.flatMap(x=>x.left===undefined?[]:[x.left]),a.map((_,i)=>i))
 assert.deepEqual(rows.flatMap(x=>x.right===undefined?[]:[x.right]),b.map((_,i)=>i))
 for(const row of rows) if(row.kind==='equal') assert.equal(a[row.left!],b[row.right!])
}
console.log('Sequence alignment: insertion, deletion, change, repeated content, 15,000 turns, reconstruction passed')
