// Mod `kiro`: hace que Claude Code sobre kiro-gateway no se corte.
//  - Un refusal reintenta una vez con el modelo de respaldo que declara Kiro.
//  - Un gateway caído se relanza con scripts/kiro-gateway.ps1.
//  - /kiro controla el gateway (estado, restart, logs, debug, models).
// Fuera de un gateway local (ANTHROPIC_BASE_URL) queda inerte.
import type { EngineInterface, Register } from 'claude-code'
import { fallbackMap, localBase, modelKey, type Status } from './kiro.ts'

// Estado del módulo: un reload vuelve a lanzar session.start y lo rehace.
// Los helpers viven a nivel de módulo: el cargador de hooks exige que toda función que reciba $ se declare ahí.
const state = { base: '', port: '', token: '', fallbacks: new Map<string, string>() }

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

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    const started = await next(e)
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

    const first = yield* next(e)
    const fallback = state.fallbacks.get(modelKey(e.model))
    if (first.stopReason !== 'refusal' || !fallback) return first
    $.ui.toast(`${e.model} cortó → reintento con ${fallback}`, { timeoutMs: 8000 })
    return yield* next({ ...e, model: fallback })
  })
}
