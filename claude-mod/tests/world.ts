// Mundo simulado bajo el mod para los tests: env, gateway (http.fetch),
// script (process.run), toasts, modelo de la sesión y el modelo (turn.step).
import type { Engine } from 'claude-code/testing'
import type { On } from 'claude-code'
import type { Status } from '../hooks/kiro.ts'

export const STATUS: Status = {
  version: 'v-test',
  uptime_seconds: 125,
  active_account: 'acc1',
  debug: { mode: 'off', dir: 'C:\\kg\\debug_logs' },
  models: [
    { id: 'claude-haiku-4.5', native_thinking: [], effort_levels: [], refusal_fallback: '' },
    { id: 'claude-sonnet-5.5', native_thinking: ['adaptive'], effort_levels: ['low', 'high'], refusal_fallback: 'claude-sonnet-5' },
  ],
}

export type World = {
  healthy: boolean
  statusCode: number
  runExit: number
  refuse: (model: string) => boolean
  /** Como el motor real: el refusal viaja en el chunk 'stop' y el resultado trae stopReason null. */
  stopOnlyInChunk: boolean
  fetches: string[]
  runs: string[][]
  toasts: string[]
  registered: string[]
  steps: string[]
  commands: { command: string; args: string }[]
  debugMode: string
}

const LOCAL_ENV = { ANTHROPIC_BASE_URL: 'http://127.0.0.1:8000', ANTHROPIC_AUTH_TOKEN: 'k-test' }

/** El mundo bajo el mod: env, gateway (fetch), script (process.run), toasts y modelo. */
export function world(on: On, opts: Partial<World> & { env?: Record<string, string> } = {}): World {
  const w: World = {
    healthy: true, statusCode: 200, stopOnlyInChunk: false, runExit: 0, refuse: m => m === 'claude-sonnet-5-5',
    fetches: [], runs: [], toasts: [], registered: [], steps: [], commands: [], debugMode: STATUS.debug.mode, ...opts,
  }
  // env vivo (no mock.env, que copia): un test puede cambiarlo entre dos session.start.
  const env: Record<string, string | undefined> = opts.env ?? LOCAL_ENV
  on('env.get', ($, e) => ({ value: env[e.name] }))
  on('session.start', ($, e) => ({ cwd: e.cwd }))
  on('command.register', ($, e) => { w.registered.push(e.name); return { value: { command: e.name } } })
  on('ui.toast', ($, e) => { w.toasts.push(e.text); return { value: undefined } })
  // $.clock.after: el mundo resuelve la espera al instante y el motor corre el callback del mod.
  on('clock.after', () => ({ value: undefined }))
  on('session.model', () => ({ value: 'claude-sonnet-5-5' }))
  on('command.run', ($, e) => { w.commands.push({ command: e.command, args: e.args }); return { text: '' } })
  on('http.fetch', ($, e) => {
    w.fetches.push(e.url)
    if (!w.healthy) throw new Error('connect ECONNREFUSED 127.0.0.1:8000')
    if (e.url.endsWith('/health')) return { value: { status: 200, ok: true, headers: {}, text: '{"status":"healthy"}' } }
    if (e.url.endsWith('/kiro/status')) {
      const ok = w.statusCode === 200
      return { value: { status: w.statusCode, ok, headers: {}, text: ok ? JSON.stringify({ ...STATUS, debug: { ...STATUS.debug, mode: w.debugMode } }) : '{"type":"error"}' } }
    }
    return { value: { status: 404, ok: false, headers: {}, text: '' } }
  })
  on('process.run', ($, e) => {
    w.runs.push([...e.argv])
    if (w.runExit === 0) w.healthy = true
    const stdout = w.runExit === 0 ? 'kiro-gateway listo en http://127.0.0.1:8000' : 'error: No encuentro kiro-gateway.exe'
    return { value: { exitCode: w.runExit, stdout, stderr: '', isStdoutTruncated: false, isStderrTruncated: false } }
  })
  on('turn.step', async function* ($, e) {
    w.steps.push(e.model)
    const stopReason = w.refuse(e.model) ? 'refusal' : 'end_turn'
    if (w.stopOnlyInChunk) yield { kind: 'stop', stopReason, usage: null } as never
    return { turnId: e.turnId, index: e.index, answer: w.refuse(e.model) ? '' : 'ok', toolUses: [], stopReason: w.stopOnlyInChunk ? null : stopReason, usage: null }
  })
  return w
}

export async function start($: Engine) {
  await $.session.start({ cwd: 'C:/repo', surface: null, isInteractive: false })
}
