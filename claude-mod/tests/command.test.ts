import { describe, expect, test, type Engine } from 'claude-code/testing'
import { start, world } from './world.ts'

async function kiro($: Engine, args = '') {
  // El Engine de pruebas tipa la entrada completa: lo que el motor estamparía cuando la persona escribe /kiro.
  const run = await $.command.run({
    command: 'kiro',
    args,
    origin: { kind: 'composer' },
    presentation: { isFullscreen: false, columns: 80 },
  })
  return run.text ?? ''
}

describe('/kiro', () => {
  test('sin argumentos muestra el estado', async ($, on) => {
    world(on)
    await start($)
    const text = await kiro($)
    expect(text).toContain('kiro-gateway v-test')
    expect(text).toContain('cuenta: acc1')
    expect(text).toContain('debug: off')
    expect(text).toContain('modelo de la sesión: claude-sonnet-5-5')
  })

  test('restart conserva el DEBUG_MODE actual', async ($, on) => {
    const w = world(on, { debugMode: 'all' })
    await start($)
    const text = await kiro($, 'restart')
    expect(w.runs.map(r => r.slice(5))).toEqual([['restart', '-DebugMode', 'all', '-Port', '8000']])
    expect(text).toBe('kiro-gateway listo en http://127.0.0.1:8000')
  })

  test('logs pasa n y valida el número', async ($, on) => {
    const w = world(on)
    await start($)
    await kiro($, 'logs')
    await kiro($, 'logs 50')
    expect(w.runs.map(r => r.slice(5))).toEqual([
      ['logs', '-Lines', '20', '-Port', '8000'],
      ['logs', '-Lines', '50', '-Port', '8000'],
    ])
    expect(await kiro($, 'logs x')).toBe('Uso: /kiro logs [n]')
  })

  test('debug reinicia con el modo pedido y valida', async ($, on) => {
    const w = world(on)
    await start($)
    const text = await kiro($, 'debug all')
    expect(w.runs.map(r => r.slice(5))).toEqual([['restart', '-DebugMode', 'all', '-Port', '8000']])
    expect(text).toContain('C:\\kg\\debug_logs')
    expect(await kiro($, 'debug mucho')).toBe('Uso: /kiro debug all|errors|off')
  })

  test('models muestra la tabla y valida el id', async ($, on) => {
    const w = world(on)
    await start($)
    expect(await kiro($, 'models')).toContain('claude-sonnet-5.5  adaptive')
    expect(await kiro($, 'models gpt-9')).toBe('gpt-9 no está en la lista de Kiro. /kiro models para verla.')
    // No lanza /model: `/model <id>` lo guarda en los settings globales y afectaría al `claude` normal.
    const text = await kiro($, 'models claude-sonnet-5-5')
    expect(text).toContain('/model, elige claude-sonnet-5-5 y pulsa s')
    expect(w.commands.filter(c => c.command === 'model')).toEqual([])
  })

  test('subcomando desconocido muestra el uso', async ($, on) => {
    world(on)
    await start($)
    expect(await kiro($, 'hola')).toBe('Uso: /kiro [restart | logs [n] | debug all|errors|off | models [id]]')
  })

  test('key que no coincide: lo dice', async ($, on) => {
    world(on, { statusCode: 401 })
    await start($)
    expect(await kiro($)).toBe('401: la key de kclaude no coincide con PROXY_API_KEY del .env del gateway.')
  })

  test('gateway apagado: sugiere /kiro restart', async ($, on) => {
    const w = world(on)
    await start($)
    w.healthy = false
    expect(await kiro($)).toBe('kiro-gateway no responde en http://127.0.0.1:8000. Prueba /kiro restart.')
  })
})
