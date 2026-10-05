// Funciones puras del mod: sin `$`, para que los tests las prueben sueltas.

/** Un modelo tal como lo devuelve GET /kiro/status. */
export type ModelInfo = {
  id: string
  native_thinking: string[]
  effort_levels: string[]
  refusal_fallback: string
}

/** El cuerpo de GET /kiro/status (DIFFERENCES §20 del gateway). */
export type Status = {
  version: string
  uptime_seconds: number
  active_account: string | null
  debug: { mode: string; dir: string }
  models: ModelInfo[]
}

export const USAGE = 'Uso: /kiro [restart | logs [n] | debug all|errors|off | models [id]]'

/**
 * Clave común para los ids de Claude Code (claude-sonnet-5-5, con o sin
 * sufijo [1m]) y los de Kiro (claude-sonnet-5.5).
 */
export function modelKey(id: string): string {
  return id.trim().toLowerCase().replace(/\[[^\]]*\]$/, '').replace(/\./g, '-')
}

/** modelKey(id) → modelo de respaldo, solo para los modelos que lo declaran. */
export function fallbackMap(models: readonly ModelInfo[]): Map<string, string> {
  const map = new Map<string, string>()
  for (const m of models) if (m.refusal_fallback) map.set(modelKey(m.id), m.refusal_fallback)
  return map
}

/** La base y el puerto si url es un gateway local; undefined en otro caso. */
export function localBase(url: string | undefined): { base: string; port: string } | undefined {
  const m = /^http:\/\/(127\.0\.0\.1|localhost):(\d+)\/?$/.exec(url ?? '')
  if (!m) return undefined
  return { base: `http://${m[1]}:${m[2]}`, port: m[2]! }
}

export function parseArgs(args: string): { sub: string; rest: string[] } {
  const parts = args.trim().split(/\s+/).filter(Boolean)
  return { sub: parts[0] ?? '', rest: parts.slice(1) }
}

function formatUptime(seconds: number): string {
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  return h > 0 ? `${h} h ${m} min` : `${m} min`
}

export function formatStatus(st: Status, sessionModel: string): string {
  return [
    `kiro-gateway ${st.version} · activo hace ${formatUptime(st.uptime_seconds)}`,
    `cuenta: ${st.active_account ?? '(ninguna)'}`,
    `debug: ${st.debug.mode} (${st.debug.dir})`,
    `modelo de la sesión: ${sessionModel}`,
  ].join('\n')
}

export function formatModels(models: readonly ModelInfo[]): string {
  const head = ['modelo', 'thinking nativo', 'effort', 'respaldo']
  const rows = models.map(m => [
    m.id,
    m.native_thinking.join(',') || '-',
    m.effort_levels.join(',') || '-',
    m.refusal_fallback || '-',
  ])
  const all = [head, ...rows]
  const widths = head.map((_, i) => Math.max(...all.map(r => r[i]!.length)))
  return all.map(r => r.map((c, i) => c.padEnd(widths[i]!)).join('  ').trimEnd()).join('\n')
}
