// Mod `kiro`: hace que Claude Code sobre kiro-gateway no se corte.
//  - Un refusal reintenta una vez con el modelo de respaldo que declara Kiro.
//  - Un gateway caído se relanza con scripts/kiro-gateway.ps1.
//  - /kiro controla el gateway (estado, restart, logs, debug, models).
// Fuera de un gateway local (ANTHROPIC_BASE_URL) queda inerte.
import type { EngineInterface, Register } from 'claude-code'
import { USAGE, fallbackMap, formatModels, formatStatus, localBase, modelKey, parseArgs, type Status } from './kiro.ts'

// Estado del módulo: un reload vuelve a lanzar session.start y lo rehace.
// Los helpers viven a nivel de módulo: el cargador de hooks exige que toda función que reciba $ se declare ahí.
const state = { base: '', port: '', token: '', fallbacks: new Map<string, string>(), retriedTurn: '' }

const scriptPath = ($: EngineInterface) =>
  `${$.plugin.root.replace(/[\\/]\.claude-plugin[\\/]?$/, '')}/../scripts/kiro-gateway.ps1`

async function healthy($: EngineInterface): Promise<boolean> {
  try {
    return (await $.http.fetch(`${state.base}/health`)).ok
  } catch {
    return false
  }
}

async function fetchStatus($: EngineInterface): Promise<Status> {
  const r = await $.http.fetch(`${state.base}/kiro/status`, { headers: { Authorization: `Bearer ${state.token}` } })
  if (r.status === 401) throw new Error('401: la key de kclaude no coincide con PROXY_API_KEY del .env del gateway.')
  if (!r.ok) throw new Error(`/kiro/status respondió ${r.status}`)
  const st = JSON.parse(r.text) as Status
  state.fallbacks = fallbackMap(st.models)
  return st
}

async function runScript($: EngineInterface, args: string[]): Promise<{ ok: boolean; text: string }> {
  const r = await $.process.run(
    ['pwsh', '-NoProfile', '-NonInteractive', '-File', scriptPath($), ...args, '-Port', state.port],
    { timeoutMs: 60_000 },
  )
  return { ok: r.exitCode === 0, text: `${r.stdout}${r.stderr}`.trim() }
}

const DEBUG_MODES = ['all', 'errors', 'off']

function switchModel($: EngineInterface, id: string): void {
  $.clock.after(0, () => {
    $.command.run({ command: 'model', args: id }).catch(err => $.ui.toast(`/model ${id} falló: ${String(err)}`))
  })
}

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
    // Reinicio: el estado es del módulo y sobrevive entre sesiones; fuera de un gateway local debe quedar vacío.
    state.base = ''
    state.port = ''
    state.token = ''
    state.fallbacks = new Map()
    state.retriedTurn = ''
    const local = localBase(await $.env.get('ANTHROPIC_BASE_URL'))
    if (!local) return started
    state.base = local.base
    state.port = local.port
    state.token = (await $.env.get('ANTHROPIC_AUTH_TOKEN')) ?? ''
    await $.command.register({
      name: 'kiro',
      description: 'Estado y control de kiro-gateway',
      argumentHint: '[restart | logs [n] | debug all|errors|off | models [id]]',
    })
    await fetchStatus($).catch(() => undefined) // sin estado: /kiro lo dirá
    return started
  })

  on('turn.step', async function* ($, e, next) {
    if (!state.base) return yield* next(e)

    if (!(await healthy($))) {
      const r = await runScript($, ['start'])
      $.ui.toast(r.ok ? 'kiro-gateway relanzado' : `kiro-gateway no arrancó: ${r.text}`, { timeoutMs: 8000 })
      if (r.ok) await fetchStatus($).catch(() => undefined)
    }

    // El motor real entrega el refusal en el chunk 'stop' y deja stopReason en null
    // en el resultado de next(); se mira en ambos sitios.
    const gen = next(e)
    let refused = false
    let first
    let done = false
    try {
      for (;;) {
        const r = await gen.next()
        if (r.done) {
          done = true
          first = r.value
          break
        }
        if (r.value.kind === 'stop' && r.value.stopReason === 'refusal') refused = true
        yield r.value
      }
    } finally {
      // yield* delegaba return()/throw(); el bucle manual debe cerrar el stream de abajo.
      if (!done) await gen.return(undefined as never)
    }
    const fallback = state.fallbacks.get(modelKey(e.model))
    if (!(refused || first.stopReason === 'refusal') || !fallback) return first
    // Si el respaldo también corta, CC reintenta el paso por su cuenta (mismo turnId, otro index):
    // un solo reintento por turno, para no gastar 4 peticiones de Kiro por un corte.
    if (state.retriedTurn === e.turnId || next.signal.aborted) return first
    state.retriedTurn = e.turnId
    $.ui.toast(`${e.model} cortó → reintento con ${fallback}`, { timeoutMs: 8000 })
    return yield* next({ ...e, model: fallback })
  })

  on('command.run', { command: 'kiro' }, async ($, e) => {
    if (!state.base) return { text: 'El mod kiro solo actúa con ANTHROPIC_BASE_URL apuntando a un gateway local (kclaude).' }
    const { sub, rest } = parseArgs(e.args)
    try {
      switch (sub) {
        case '':
          return { text: formatStatus(await fetchStatus($), await $.session.model()) }
        case 'restart': {
          const st = await fetchStatus($).catch(() => undefined)
          const r = await runScript($, ['restart', ...(st ? ['-DebugMode', st.debug.mode] : [])])
          if (r.ok) await fetchStatus($).catch(() => undefined)
          return { text: r.text }
        }
        case 'logs': {
          const n = rest[0] === undefined ? 20 : Number(rest[0])
          if (!Number.isInteger(n) || n <= 0) return { text: 'Uso: /kiro logs [n]' }
          return { text: (await runScript($, ['logs', '-Lines', String(n)])).text }
        }
        case 'debug': {
          const mode = rest[0] ?? ''
          if (!DEBUG_MODES.includes(mode)) return { text: 'Uso: /kiro debug all|errors|off' }
          const r = await runScript($, ['restart', '-DebugMode', mode])
          if (!r.ok) return { text: r.text }
          const st = await fetchStatus($)
          return { text: `${r.text}\ndebug: ${st.debug.mode} → ${st.debug.dir}` }
        }
        case 'models': {
          const st = await fetchStatus($)
          const id = rest[0]
          if (!id) return { text: formatModels(st.models) }
          if (!st.models.some(m => modelKey(m.id) === modelKey(id))) {
            return { text: `${id} no está en la lista de Kiro. /kiro models para verla.` }
          }
          // El host rechaza un command.run anidado dentro de este hook (esperaría a su propio turno):
          // se lanza en un temporizador ($.clock.after), ya fuera del hook, y /model corre cuando la sesión queda libre.
          switchModel($, id)
          return { text: `Cambiando el modelo de la sesión a ${id}…` }
        }
        default:
          return { text: USAGE }
      }
    } catch (err) {
      if (!(await healthy($))) return { text: `kiro-gateway no responde en ${state.base}. Prueba /kiro restart.` }
      return { text: err instanceof Error ? err.message : String(err) }
    }
  })
}
