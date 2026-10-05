import { describe, expect, test } from 'claude-code/testing'
import { fallbackMap, formatModels, formatStatus, localBase, modelKey, parseArgs, type ModelInfo, type Status } from '../hooks/kiro.ts'

const MODELS: ModelInfo[] = [
  { id: 'claude-haiku-4.5', native_thinking: [], effort_levels: [], refusal_fallback: '' },
  { id: 'claude-sonnet-5.5', native_thinking: ['adaptive', 'between_tools'], effort_levels: ['low', 'high'], refusal_fallback: 'claude-sonnet-5' },
]

describe('kiro.ts', () => {
  test('modelKey iguala ids de Claude Code y de Kiro', () => {
    expect(modelKey('claude-sonnet-5.5')).toBe('claude-sonnet-5-5')
    expect(modelKey('claude-sonnet-5-5')).toBe('claude-sonnet-5-5')
    expect(modelKey('Claude-Sonnet-5-5[1m]')).toBe('claude-sonnet-5-5')
  })

  test('fallbackMap solo guarda los modelos con respaldo', () => {
    const m = fallbackMap(MODELS)
    expect(m.size).toBe(1)
    expect(m.get('claude-sonnet-5-5')).toBe('claude-sonnet-5')
  })

  test('localBase acepta solo un gateway local', () => {
    expect(localBase('http://127.0.0.1:8000')).toEqual({ base: 'http://127.0.0.1:8000', port: '8000' })
    expect(localBase('http://localhost:9001/')).toEqual({ base: 'http://localhost:9001', port: '9001' })
    expect(localBase('https://api.anthropic.com')).toBeUndefined()
    expect(localBase('http://10.0.0.5:8000')).toBeUndefined()
    expect(localBase(undefined)).toBeUndefined()
  })

  test('parseArgs separa el subcomando', () => {
    expect(parseArgs('')).toEqual({ sub: '', rest: [] })
    expect(parseArgs('  logs   50 ')).toEqual({ sub: 'logs', rest: ['50'] })
  })

  test('formatStatus y formatModels', () => {
    const st: Status = { version: 'v0.4.0', uptime_seconds: 3725, active_account: 'acc1', debug: { mode: 'errors', dir: 'C:\\kg\\debug_logs' }, models: MODELS }
    const s = formatStatus(st, 'claude-sonnet-5-5')
    expect(s).toContain('kiro-gateway v0.4.0')
    expect(s).toContain('1 h 2 min')
    expect(s).toContain('acc1')
    expect(s).toContain('errors (C:\\kg\\debug_logs)')
    expect(s).toContain('claude-sonnet-5-5')
    const t = formatModels(MODELS).split('\n')
    expect(t).toHaveLength(3)
    expect(t[0]).toMatch(/^modelo\s+thinking nativo\s+effort\s+respaldo$/)
    expect(t[1]).toMatch(/^claude-haiku-4\.5\s+-\s+-\s+-$/)
    expect(t[2]).toMatch(/^claude-sonnet-5\.5\s+adaptive,between_tools\s+low,high\s+claude-sonnet-5$/)
  })
})
