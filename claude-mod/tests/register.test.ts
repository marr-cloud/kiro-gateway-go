import { describe, expect, test, type Engine } from 'claude-code/testing'
import { start, world } from './world.ts'

async function step($: Engine, model: string) {
  const s = $.turn.step({ turnId: 't1', index: 0, model, messageCount: 1 })
  // El motor de tests no rellena `s.result`: el resultado es el valor de retorno del iterador.
  for (;;) {
    const it = await s.next()
    if (it.done) return it.value
  }
}

describe('arranque', () => {
  test('inerte si la sesión no va por un gateway local', async ($, on) => {
    const w = world(on, { env: { ANTHROPIC_BASE_URL: 'https://api.anthropic.com' } })
    await start($)
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.registered).toEqual([])
    expect(w.fetches).toEqual([])
    expect(w.steps).toEqual(['claude-sonnet-5-5'])
    expect(r.stopReason).toBe('refusal')
  })

  test('una sesión no local tras una local reinicia el estado y deja el mod inerte', async ($, on) => {
    const env: Record<string, string> = { ANTHROPIC_BASE_URL: 'http://127.0.0.1:8000', ANTHROPIC_AUTH_TOKEN: 'k-test' }
    const w = world(on, { env })
    await start($)
    expect(w.registered).toEqual(['kiro'])
    env.ANTHROPIC_BASE_URL = 'https://api.anthropic.com' // el mock de env lee el objeto en cada get
    await start($)
    w.fetches.length = 0
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.fetches).toEqual([])
    expect(w.steps).toEqual(['claude-sonnet-5-5'])
    expect(r.stopReason).toBe('refusal')
  })

  test('registra /kiro y pide el estado una vez', async ($, on) => {
    const w = world(on)
    await start($)
    expect(w.registered).toEqual(['kiro'])
    expect(w.fetches).toEqual(['http://127.0.0.1:8000/kiro/status'])
  })
})

describe('gateway caído', () => {
  test('lo relanza con el script y sigue', async ($, on) => {
    const w = world(on)
    await start($)
    w.healthy = false
    const r = await step($, 'claude-haiku-4-5')
    expect(w.runs).toHaveLength(1)
    expect(w.runs[0]!.slice(0, 4)).toEqual(['pwsh', '-NoProfile', '-NonInteractive', '-File'])
    expect(w.runs[0]![4]).toMatch(/[\\/]\.\.[\\/]scripts[\\/]kiro-gateway\.ps1$/)
    expect(w.runs[0]!.slice(5)).toEqual(['start', '-Port', '8000'])
    expect(w.toasts).toEqual(['kiro-gateway relanzado'])
    expect(w.steps).toEqual(['claude-haiku-4-5'])
    expect(r.stopReason).toBe('end_turn')
  })

  test('si no arranca avisa y deja seguir la petición', async ($, on) => {
    const w = world(on, { runExit: 1 })
    await start($)
    w.healthy = false
    await step($, 'claude-haiku-4-5')
    expect(w.runs).toHaveLength(1)
    expect(w.toasts).toEqual(['kiro-gateway no arrancó: error: No encuentro kiro-gateway.exe'])
    expect(w.steps).toEqual(['claude-haiku-4-5'])
  })
})

describe('refusal', () => {
  test('reintenta una vez con el respaldo de Kiro (id de Claude Code)', async ($, on) => {
    const w = world(on)
    await start($)
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.steps).toEqual(['claude-sonnet-5-5', 'claude-sonnet-5'])
    expect(w.toasts).toEqual(['claude-sonnet-5-5 cortó → reintento con claude-sonnet-5'])
    expect(r.stopReason).toBe('end_turn')
    expect(r.answer).toBe('ok')
  })

  test('detecta el refusal que solo viaja en el chunk stop (motor real)', async ($, on) => {
    const w = world(on, { stopOnlyInChunk: true })
    await start($)
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.steps).toEqual(['claude-sonnet-5-5', 'claude-sonnet-5'])
    expect(r.answer).toBe('ok')
  })

  test('no reintenta dos veces si el respaldo también corta', async ($, on) => {
    const w = world(on, { refuse: () => true })
    await start($)
    const r = await step($, 'claude-sonnet-5-5')
    expect(w.steps).toEqual(['claude-sonnet-5-5', 'claude-sonnet-5'])
    expect(r.stopReason).toBe('refusal')
  })

  test('sin respaldo no reintenta', async ($, on) => {
    const w = world(on, { refuse: () => true })
    await start($)
    await step($, 'claude-haiku-4-5')
    expect(w.steps).toEqual(['claude-haiku-4-5'])
    expect(w.toasts).toEqual([])
  })
})
