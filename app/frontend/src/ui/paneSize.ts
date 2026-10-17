export function paneBounds(total: number, min: number, max: number, minRest: number, minRatio = 0, maxRatio = 1) {
  const upper = Math.max(0, Math.min(max, total - minRest, total * maxRatio))
  return { min: Math.min(upper, Math.max(min, total * minRatio)), max: upper }
}

export const clampPane = (width: number, bounds: { min: number; max: number }) => Math.round(Math.max(bounds.min, Math.min(bounds.max, width)))
