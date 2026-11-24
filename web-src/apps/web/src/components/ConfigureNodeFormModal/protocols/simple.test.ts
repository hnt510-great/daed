import { describe, expect, it, vi } from 'vitest'

vi.stubGlobal('location', { protocol: 'http:', hostname: '127.0.0.1' })

describe('naive+https protocol', () => {
  it('generates and parses naive+https links', async () => {
    const { naiveHttpsProtocol } = await import('./simple')
    const link = naiveHttpsProtocol.generateLink({
      name: 'MyNaive',
      host: 'proxy.example.com',
      port: 443,
      username: 'alice',
      password: 'secret',
      sni: 'cdn.example.com',
      padding: true,
    })
    expect(link).toContain('naive+https://alice:secret@proxy.example.com:443')
    const parsed = naiveHttpsProtocol.parseLink!(link)
    expect(parsed).toMatchObject({
      name: 'MyNaive',
      host: 'proxy.example.com',
      port: 443,
      username: 'alice',
      password: 'secret',
      sni: 'cdn.example.com',
      padding: true,
    })
  })

  it('rejects naive+quic with an explicit unsupported transport message', async () => {
    const { naiveHttpsProtocol } = await import('./simple')
    expect(() => naiveHttpsProtocol.parseLink!('naive+quic://u:p@example.com:443')).toThrow(/naive\+quic/i)
  })
})
