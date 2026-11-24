import type { GenerateURLParams } from '@daeuniverse/dae-node-parser'
import type { ProtocolConfig } from './types'
import { generateURL, parseHTTPUrl, parseSocks5Url } from '@daeuniverse/dae-node-parser'
import { z } from 'zod'

import { DEFAULT_HTTP_FORM_VALUES, DEFAULT_SOCKS5_FORM_VALUES, httpSchema, socks5Schema } from '~/constants'

// ============================================================================
// HTTP Protocol
// ============================================================================

const httpFormSchema = httpSchema.extend({
  protocol: z.enum(['http', 'https']),
})

export type HTTPFormValues = z.infer<typeof httpFormSchema>

function generateHTTPLink(data: HTTPFormValues): string {
  const params: GenerateURLParams = {
    protocol: data.protocol,
    host: data.host,
    port: data.port,
    hash: data.name,
  }

  if (data.username && data.password) {
    params.username = data.username
    params.password = data.password
  }

  return generateURL(params)
}

export const httpProtocol: ProtocolConfig<HTTPFormValues> = {
  id: 'http',
  label: 'HTTP',
  schema: httpFormSchema,
  defaultValues: {
    protocol: 'http',
    ...DEFAULT_HTTP_FORM_VALUES,
  },
  generateLink: generateHTTPLink,
  parseLink: parseHTTPUrl,
  fields: [
    {
      name: 'protocol',
      label: 'configureNode.protocol',
      type: 'select',
      options: [
        { label: 'HTTP', value: 'http' },
        { label: 'HTTPS', value: 'https' },
      ],
    },
    {
      name: 'name',
      label: 'configureNode.name',
      type: 'text',
    },
    {
      name: 'host',
      label: 'configureNode.host',
      type: 'text',
      required: true,
    },
    {
      name: 'port',
      label: 'configureNode.port',
      type: 'number',
      required: true,
      min: 0,
      max: 65535,
    },
    {
      name: 'username',
      label: 'configureNode.username',
      type: 'text',
    },
    {
      name: 'password',
      label: 'configureNode.password',
      type: 'text',
    },
  ],
}

// ============================================================================
// SOCKS5 Protocol
// ============================================================================

export type Socks5FormValues = z.infer<typeof socks5Schema>

function generateSocks5Link(data: Socks5FormValues): string {
  const params: GenerateURLParams = {
    protocol: 'socks5',
    host: data.host,
    port: data.port,
    hash: data.name,
  }

  if (data.username && data.password) {
    params.username = data.username
    params.password = data.password
  }

  return generateURL(params)
}

export const socks5Protocol: ProtocolConfig<Socks5FormValues> = {
  id: 'socks5',
  label: 'SOCKS5',
  schema: socks5Schema,
  defaultValues: DEFAULT_SOCKS5_FORM_VALUES,
  generateLink: generateSocks5Link,
  parseLink: parseSocks5Url,
  fields: [
    {
      name: 'name',
      label: 'configureNode.name',
      type: 'text',
    },
    {
      name: 'host',
      label: 'configureNode.host',
      type: 'text',
      required: true,
    },
    {
      name: 'port',
      label: 'configureNode.port',
      type: 'number',
      required: true,
      min: 0,
      max: 65535,
    },
    {
      name: 'username',
      label: 'configureNode.username',
      type: 'text',
    },
    {
      name: 'password',
      label: 'configureNode.password',
      type: 'text',
    },
  ],
}

// ============================================================================
// NaiveProxy (naive+https) Protocol
// ============================================================================

const naiveHttpsSchema = z.object({
  name: z.string().optional(),
  host: z.string().min(1),
  port: z.coerce.number().int().min(1).max(65535),
  username: z.string().optional(),
  password: z.string().optional(),
  sni: z.string().optional(),
  padding: z.boolean().optional(),
})

export type NaiveHttpsFormValues = z.infer<typeof naiveHttpsSchema>

function generateNaiveHttpsLink(data: NaiveHttpsFormValues): string {
  const auth = data.username
    ? `${encodeURIComponent(data.username)}:${encodeURIComponent(data.password || '')}@`
    : ''
  const host = data.host.includes(':') && !data.host.startsWith('[') ? `[${data.host}]` : data.host
  const params = new URLSearchParams()
  if (data.sni) params.set('sni', data.sni)
  if (data.padding !== undefined) params.set('padding', String(data.padding))
  const qs = params.toString()
  const hash = data.name ? `#${encodeURIComponent(data.name)}` : ''
  return `naive+https://${auth}${host}:${data.port}${qs ? `?${qs}` : ''}${hash}`
}

function parseNaiveHttpsUrl(raw: string): NaiveHttpsFormValues | null {
  const trimmed = raw.trim()
  if (trimmed.toLowerCase().startsWith('naive+quic://')) {
    throw new Error('naive+quic transport is not supported in this version; use naive+https')
  }
  if (!trimmed.toLowerCase().startsWith('naive+https://')) {
    return null
  }
  const normalized = trimmed.replace(/^naive\+https:\/\//i, 'https://')
  const u = new URL(normalized)
  return {
    name: u.hash ? decodeURIComponent(u.hash.slice(1)) : '',
    host: u.hostname.replace(/^\[|\]$/g, ''),
    port: Number(u.port || '443'),
    username: decodeURIComponent(u.username || ''),
    password: decodeURIComponent(u.password || ''),
    sni: u.searchParams.get('sni') || '',
    padding: u.searchParams.get('padding') !== 'false',
  }
}

export const naiveHttpsProtocol: ProtocolConfig<NaiveHttpsFormValues> = {
  id: 'naive+https',
  label: 'Naive (HTTPS)',
  schema: naiveHttpsSchema,
  defaultValues: {
    name: '',
    host: '',
    port: 443,
    username: '',
    password: '',
    sni: '',
    padding: true,
  },
  generateLink: generateNaiveHttpsLink,
  parseLink: parseNaiveHttpsUrl,
  fields: [
    { name: 'name', label: 'configureNode.name', type: 'text' },
    { name: 'host', label: 'configureNode.host', type: 'text', required: true },
    { name: 'port', label: 'configureNode.port', type: 'number', required: true, min: 1, max: 65535 },
    { name: 'username', label: 'configureNode.username', type: 'text' },
    { name: 'password', label: 'configureNode.password', type: 'text' },
    { name: 'sni', label: 'SNI', type: 'text' },
    { name: 'padding', label: 'Padding', type: 'checkbox' },
  ],
}
