/** Keep disabled and unknown entries in the saved order; only rendering filters them out. */
export function agentOrder(ids: readonly string[], saved: readonly string[], name: (id: string) => string): string[] {
  const alphabetical = [...new Set(ids)].sort((a, b) => name(a).localeCompare(name(b), 'en', { sensitivity: 'base', numeric: true }) || a.localeCompare(b))
  return saved.length ? [...new Set([...saved, ...alphabetical])] : alphabetical
}

export function moveAgent(order: readonly string[], id: string, target: string, after = false): string[] {
  if (id === target || !order.includes(id) || !order.includes(target)) return [...order]
  const next = order.filter(item => item !== id)
  next.splice(next.indexOf(target) + Number(after), 0, id)
  return next
}

/** Each product is independently ordered; profiles of that product share one row. */
export function orderAgentProducts<T extends { harness: string; variant: string }>(products: readonly T[], saved: readonly string[], name: (id: string) => string): T[] {
  return agentOrder(products.map(product => `${product.harness}/${product.variant}`), saved, name).flatMap(id => products.filter(product => `${product.harness}/${product.variant}` === id))
}
