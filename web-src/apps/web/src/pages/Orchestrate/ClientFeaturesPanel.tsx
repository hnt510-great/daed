import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useConfigsQuery, useDNSsQuery, useGroupsQuery, useNodesQuery, useRoutingsQuery, useSubscriptionsQuery } from '~/apis'
import { Button } from '~/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '~/components/ui/card'
import {
  type NodePathProbeResult,
  type PolicyPreview,
  type TraceRouteResult,
  useClientFeatureMutations,
  useConfigSnapshotsQuery,
  useDnsListBindingsQuery,
  useGroupFailoverQuery,
  useLanDevicesQuery,
  usePolicyDraftQuery,
  useProxyChainsQuery,
  useRuleListsQuery,
  useSubscriptionEnhancedQuery,
  useSystemApplyStateQuery,
} from '~/apis/clientFeatures'

type TabId = 'acl' | 'lists' | 'subscriptions' | 'chains' | 'failover' | 'diagnostics'

interface ACLSource {
  kind: 'mac' | 'ip' | 'cidr' | 'range'
  value: string
  label?: string
}

interface DeviceACLRule {
  id: string
  name: string
  enabled: boolean
  sources: ACLSource[]
  protocol: string
  ports: string
  action: 'direct' | 'block' | 'group' | 'inherit'
  targetGroup: string
  blockProxyPorts?: string
}

interface VisualPolicy {
  defaultAction: 'direct' | 'block' | 'group'
  defaultGroup: string
  skipProxyPorts: string
  allowProxyPorts: string
  blockProxyPorts: string
  acls: DeviceACLRule[]
  listRules: { id: string; enabled: boolean; listId: number; action: string; targetGroup: string }[]
  extraRawRules?: string
}

const DEFAULT_VISUAL_POLICY: VisualPolicy = {
  defaultAction: 'direct',
  defaultGroup: '',
  skipProxyPorts: '',
  allowProxyPorts: '',
  blockProxyPorts: '',
  acls: [],
  listRules: [],
  extraRawRules: '',
}

type RoutingPresetId = 'cn_direct' | 'gfw' | 'cn_proxy' | 'global_proxy' | 'global_direct' | 'custom'

function detectRoutingPreset(p: VisualPolicy): RoutingPresetId {
  const extra = (p.extraRawRules || '').toLowerCase()
  const hasGeoCnDirect = extra.includes('geoip:cn') && extra.includes('-> direct')
  const hasGeoCnProxy = extra.includes('geoip:cn') && extra.includes('-> ' + (p.defaultGroup || 'proxy').toLowerCase())
  const hasGfw = extra.includes('geosite:gfw')
  if (hasGfw && p.defaultAction === 'direct') return 'gfw'
  if (hasGeoCnDirect && p.defaultAction === 'group') return 'cn_direct'
  if (hasGeoCnProxy && p.defaultAction === 'direct') return 'cn_proxy'
  if (!hasGeoCnDirect && !hasGfw && !hasGeoCnProxy && p.defaultAction === 'group') return 'global_proxy'
  if (!hasGeoCnDirect && !hasGfw && !hasGeoCnProxy && p.defaultAction === 'direct') return 'global_direct'
  return 'custom'
}

function applyRoutingPreset(p: VisualPolicy, preset: RoutingPresetId, fallbackGroup: string): VisualPolicy {
  const grp = p.defaultGroup || fallbackGroup || 'proxy'
  // Preserve system/private/node-direct lines, replace only geo split lines.
  const preserved = (p.extraRawRules || '')
    .split('\n')
    .map((l) => l.trim())
    .filter((l) => l && !l.includes('geoip:cn') && !l.includes('geosite:cn') && !l.includes('geosite:gfw') && !l.includes('suffix:cn'))
  if (!preserved.some((l) => l.includes('geoip:private'))) {
    preserved.unshift('dip(geoip:private) -> direct')
  }
  if (!preserved.some((l) => l.includes('must_direct'))) {
    preserved.unshift('pname(NetworkManager, systemd-resolved, dnsmasq) -> must_direct')
  }
  switch (preset) {
    case 'cn_direct':
      return {
        ...p,
        defaultAction: 'group',
        defaultGroup: grp,
        extraRawRules: [...preserved, 'domain(suffix:cn) -> direct', 'dip(geoip:cn) -> direct', 'domain(geosite:cn) -> direct'].join('\n'),
      }
    case 'gfw':
      return {
        ...p,
        defaultAction: 'direct',
        defaultGroup: grp,
        extraRawRules: [...preserved, `domain(geosite:gfw) -> ${grp}`].join('\n'),
      }
    case 'cn_proxy':
      return {
        ...p,
        defaultAction: 'direct',
        defaultGroup: grp,
        extraRawRules: [...preserved, `domain(suffix:cn) -> ${grp}`, `dip(geoip:cn) -> ${grp}`, `domain(geosite:cn) -> ${grp}`].join('\n'),
      }
    case 'global_proxy':
      return {
        ...p,
        defaultAction: 'group',
        defaultGroup: grp,
        extraRawRules: preserved.join('\n'),
      }
    case 'global_direct':
      return {
        ...p,
        defaultAction: 'direct',
        defaultGroup: grp,
        extraRawRules: preserved.join('\n'),
      }
    default:
      return p
  }
}

export function ClientFeaturesPanel() {
  const { i18n } = useTranslation()
  const isZh = i18n.language.toLowerCase().startsWith('zh')
  const t = (zh: string, en: string) => (isZh ? zh : en)

  const [tab, setTab] = useState<TabId>('acl')
  const [collapsed, setCollapsed] = useState(false)
  const [notice, setNotice] = useState<string>('')
  const [errMsg, setErrMsg] = useState<string>('')

  const { data: configs } = useConfigsQuery()
  const { data: dnss } = useDNSsQuery()
  const { data: routings } = useRoutingsQuery()
  const { data: groups } = useGroupsQuery()
  const { data: nodes } = useNodesQuery()
  const { data: subs } = useSubscriptionsQuery()

  const selectedConfig = configs?.configs.find((c) => c.selected)
  const selectedDns = dnss?.dnss.find((d) => d.selected)
  const selectedRouting = routings?.routings.find((r) => r.selected)

  const { data: applyState } = useSystemApplyStateQuery()
  const { data: lanDevices, refetch: refetchLan } = useLanDevicesQuery()
  const { data: ruleLists } = useRuleListsQuery()
  const { data: draftState } = usePolicyDraftQuery(selectedRouting?.id)
  const { data: snapshots } = useConfigSnapshotsQuery()
  const { data: proxyChains } = useProxyChainsQuery()

  const [selectedGroupId, setSelectedGroupId] = useState<string>('')
  const [selectedSubId, setSelectedSubId] = useState<string>('')

  useEffect(() => {
    if (!selectedGroupId && groups?.groups.length) setSelectedGroupId(groups.groups[0].id)
  }, [groups, selectedGroupId])

  useEffect(() => {
    if (!selectedSubId && subs?.subscriptions.length) setSelectedSubId(subs.subscriptions[0].id)
  }, [subs, selectedSubId])

  const { data: failoverStatus } = useGroupFailoverQuery(selectedGroupId || undefined)
  const { data: subEnhanced } = useSubscriptionEnhancedQuery(selectedSubId || undefined)
  const { data: dnsBindings } = useDnsListBindingsQuery(selectedDns?.id)

  const m = useClientFeatureMutations()

  // --- Visual Policy Draft State ---
  const [policy, setPolicy] = useState<VisualPolicy>(DEFAULT_VISUAL_POLICY)
  const [draftDirty, setDraftDirty] = useState(false)
  const [preview, setPreview] = useState<PolicyPreview | null>(null)

  useEffect(() => {
    if (draftState?.policyJson) {
      try {
        const parsed = JSON.parse(draftState.policyJson) as Partial<VisualPolicy>
        setPolicy({ ...DEFAULT_VISUAL_POLICY, ...parsed })
        setDraftDirty(false)
      } catch {
        // ignore invalid stored json
      }
    }
  }, [draftState?.policyJson, draftState?.draftRevision])

  // --- Rule List form state ---
  const [rlEditingId, setRlEditingId] = useState<string | null>(null)
  const [rlEditingRev, setRlEditingRev] = useState<number | undefined>(undefined)
  const [rlName, setRlName] = useState('')
  const [rlKind, setRlKind] = useState('domain')
  const [rlSourceType, setRlSourceType] = useState('static')
  const [rlUrl, setRlUrl] = useState('')
  const [rlDownloadMode, setRlDownloadMode] = useState('auto')
  const [rlDownloadGroup, setRlDownloadGroup] = useState('')
  const [rlText, setRlText] = useState('')

  // --- Proxy Chain form state ---
  const [chainName, setChainName] = useState('')
  const [chainHop1, setChainHop1] = useState('')
  const [chainHop2, setChainHop2] = useState('')
  const [chainAttachGroup, setChainAttachGroup] = useState('')

  // --- Failover form state ---
  const [foEnabled, setFoEnabled] = useState(false)
  const [foPrimary, setFoPrimary] = useState('')
  const [foBackups, setFoBackups] = useState('')
  const [foAutoFailback, setFoAutoFailback] = useState(true)
  const [foProbeUrl, setFoProbeUrl] = useState('http://www.gstatic.com/generate_204')
  const [foProbeDns, setFoProbeDns] = useState('1.1.1.1:53')

  useEffect(() => {
    if (failoverStatus) {
      setFoEnabled(failoverStatus.enabled)
      setFoPrimary(failoverStatus.primaryMemberKey)
      setFoBackups(failoverStatus.backupMemberKeys.join(','))
      setFoAutoFailback(failoverStatus.autoFailback)
      setFoProbeUrl(failoverStatus.probeUrl)
      setFoProbeDns(failoverStatus.probeDns)
    }
  }, [failoverStatus])

  // --- Subscription enhanced form state ---
  const [subFormat, setSubFormat] = useState('auto')
  const [subUA, setSubUA] = useState('')
  const [subDlMode, setSubDlMode] = useState('auto')
  const [subDlGroup, setSubDlGroup] = useState('')
  const [subInc, setSubInc] = useState('')
  const [subExc, setSubExc] = useState('')
  const [subPriority, setSubPriority] = useState('exclude_first')

  useEffect(() => {
    if (subEnhanced) {
      setSubFormat(subEnhanced.format || 'auto')
      setSubUA(subEnhanced.userAgent || '')
      setSubDlMode(subEnhanced.downloadMode || 'auto')
      setSubDlGroup(subEnhanced.downloadGroupName || '')
      setSubInc(subEnhanced.includeKeywords || '')
      setSubExc(subEnhanced.excludeKeywords || '')
      setSubPriority(subEnhanced.filterPriority || 'exclude_first')
    }
  }, [subEnhanced])

  // --- Diagnostics state ---
  const [diagTargetKind, setDiagTargetKind] = useState<'node' | 'chain'>('node')
  const [diagTargetId, setDiagTargetId] = useState('')
  const [diagProbeType, setDiagProbeType] = useState('url')
  const [diagDest, setDiagDest] = useState('http://www.gstatic.com/generate_204')
  const [probeResult, setProbeResult] = useState<NodePathProbeResult | null>(null)

  const [traceNet, setTraceNet] = useState('tcp')
  const [traceDomain, setTraceDomain] = useState('')
  const [traceDstIp, setTraceDstIp] = useState('')
  const [traceDstPort, setTraceDstPort] = useState('443')
  const [traceSrcIp, setTraceSrcIp] = useState('')
  const [traceResult, setTraceResult] = useState<TraceRouteResult | null>(null)

  const runAction = async (fn: () => Promise<void>) => {
    setErrMsg('')
    setNotice('')
    try {
      await fn()
    } catch (e: unknown) {
      setErrMsg(e instanceof Error ? e.message : String(e))
    }
  }

  const isVisualMode = draftState?.editMode === 'visual'
  const fieldCls = 'rounded-md border border-input bg-background px-2.5 py-1.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-ring'

  // Compute 4 explicit states: draft / pending_apply / running / apply_failed
  const statusBadge = (() => {
    if (applyState?.lastApplyStatus === 'apply_failed') {
      return { label: t('应用失败', 'Apply Failed'), cls: 'bg-destructive/15 text-destructive border border-destructive/30' }
    }
    if (draftDirty) {
      return { label: t('草稿未保存', 'Draft'), cls: 'bg-amber-500/15 text-amber-600 dark:text-amber-400 border border-amber-500/30' }
    }
    if (preview?.hasEffectiveChanges) {
      return { label: t('待应用', 'Pending Apply'), cls: 'bg-primary/15 text-primary border border-primary/30' }
    }
    if (applyState?.running) {
      return { label: t('运行中', 'Running'), cls: 'bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border border-emerald-500/30' }
    }
    return { label: t('已停止', 'Stopped'), cls: 'bg-muted text-muted-foreground border border-border' }
  })()

  const tabs: { id: TabId; label: string }[] = [
    { id: 'acl', label: t('设备策略与预览', 'Device ACL & Preview') },
    { id: 'lists', label: t('规则列表与 DNS', 'Rule Lists & DNS') },
    { id: 'subscriptions', label: t('增强订阅', 'Enhanced Subscriptions') },
    { id: 'chains', label: t('代理链', 'Proxy Chains') },
    { id: 'failover', label: t('分组主备', 'Group Failover') },
    { id: 'diagnostics', label: t('诊断与规则追踪', 'Diagnostics & Trace') },
  ]

  return (
    <Card className="border-border/80 shadow-sm" data-testid="client-features-panel">
      {/* Status & Apply Lifecycle Header */}
      <CardHeader className="pb-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <CardTitle className="text-base font-semibold text-primary">
              {t('客户端策略与运维中心', 'Client Policy & Operations')}
            </CardTitle>
            <span className={`px-2 py-0.5 rounded-md text-xs font-medium ${statusBadge.cls}`} data-testid="policy-status-badge">
              {statusBadge.label}
            </span>
            {applyState && (
              <span className="text-xs text-muted-foreground">
                {t('运行代', 'Gen')}: {applyState.generation} · {t('摘要', 'Digest')}: {applyState.activeDigest ? applyState.activeDigest.slice(0, 10) : '-'}
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {selectedRouting && (
              <label className="flex items-center gap-1.5 text-xs cursor-pointer rounded-md border border-border bg-muted/40 px-2.5 py-1">
                <input
                  type="checkbox"
                  checked={isVisualMode}
                  onChange={(e) => runAction(async () => {
                    const nextMode = e.target.checked ? 'visual' : 'raw'
                    await m.setRoutingEditMode.mutateAsync({ routingId: selectedRouting.id, editMode: nextMode })
                    setNotice(
                      nextMode === 'visual'
                        ? t('已开启可视化策略模式（点「完整应用」生效）', 'Visual policy mode enabled (click Apply to activate)')
                        : t('已切回原生手写模式，上方 Routing 卡片现已解锁可编辑（点「完整应用」生效）', 'Switched to native raw mode; upper Routing card is now editable'),
                    )
                  })}
                />
                <span>{t('可视化策略接管路由', 'Visual Policy Mode')}</span>
              </label>
            )}
            {selectedConfig && (
              <label className="flex items-center gap-1.5 text-xs cursor-pointer text-muted-foreground">
                <input
                  type="checkbox"
                  checked={!!applyState?.autoApplyEnabled}
                  onChange={(e) => runAction(async () => {
                    await m.setConfigAutoApply.mutateAsync({ configId: selectedConfig.id, autoApply: e.target.checked })
                    setNotice(t('自动应用设置已更新', 'Auto-apply updated'))
                  })}
                />
                {t('定时自动应用', 'Auto-apply')}
              </label>
            )}
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => runAction(async () => {
                if (isVisualMode && draftDirty && selectedRouting) {
                  await m.savePolicyDraft.mutateAsync({
                    routingId: selectedRouting.id,
                    expectedRevision: draftState?.draftRevision,
                    policyJson: JSON.stringify(policy),
                  })
                  setDraftDirty(false)
                }
                const p = await m.previewPolicy.mutateAsync({})
                setPreview(p)
                setNotice(t('候选预览已生成（无运行副作用）', 'Candidate preview ready (no runtime side effects)'))
              })}
            >
              {t('预览候选', 'Preview')}
            </Button>
            <Button
              type="button"
              variant="default"
              size="sm"
              onClick={() => runAction(async () => {
                if (isVisualMode && draftDirty && selectedRouting) {
                  await m.savePolicyDraft.mutateAsync({
                    routingId: selectedRouting.id,
                    expectedRevision: draftState?.draftRevision,
                    policyJson: JSON.stringify(policy),
                  })
                  setDraftDirty(false)
                }
                const p = await m.previewPolicy.mutateAsync({})
                setPreview(p)
                const res = await m.applyPolicyRevision.mutateAsync({ expectedDigest: p.digest })
                setNotice(`${t('应用结果', 'Apply')}: ${res.status} (${res.message})`)
              })}
            >
              {t('完整应用', 'Apply')}
            </Button>
            {snapshots && snapshots.length > 1 && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => runAction(async () => {
                  const prev = snapshots.find((s) => !s.active)
                  if (!prev) return
                  const res = await m.rollbackPolicy.mutateAsync(prev.id)
                  setNotice(res.message)
                })}
              >
                {t('回退快照', 'Rollback')}
              </Button>
            )}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={() => setCollapsed((v) => !v)}
            >
              {collapsed ? t('展开', 'Expand') : t('收起', 'Collapse')}
            </Button>
          </div>
        </div>
      </CardHeader>

      {!collapsed && (
      <CardContent className="pt-0">
      {notice && <div className="mb-3 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-700 dark:text-emerald-300">{notice}</div>}
      {errMsg && <div className="mb-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">{errMsg}</div>}
      {applyState?.lastApplyError && (
        <div className="mb-3 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {t('上次应用错误', 'Last Apply Error')}: {applyState.lastApplyError}
        </div>
      )}

      {/* Tab Navigation */}
      <div className="flex flex-wrap gap-1.5 border-b border-border pb-2.5 mb-4">
        {tabs.map((item) => (
          <Button
            key={item.id}
            type="button"
            size="sm"
            variant={tab === item.id ? 'default' : 'secondary'}
            onClick={() => setTab(item.id)}
            className="h-7 px-3 text-xs"
          >
            {item.label}
          </Button>
        ))}
      </div>

      {/* TAB 1: Device ACL & Preview */}
      {tab === 'acl' && (
        <div className="space-y-4">
          {!isVisualMode && (
            <div className="rounded-md border border-border bg-muted/40 px-3 py-2 text-xs text-muted-foreground">
              {t('当前已关闭可视化策略接管（原生手写模式），可直接在上方 Routing 卡片编辑规则；勾选右上角「可视化策略接管路由」即可切回可视化模式。', 'Visual policy mode is off (native raw mode). Edit rules directly in the upper Routing card, or re-enable Visual Policy Mode above.')}
            </div>
          )}
          <div className={`grid grid-cols-1 md:grid-cols-5 gap-3 text-xs ${!isVisualMode ? 'opacity-60 pointer-events-none' : ''}`}>
            <label className="flex flex-col gap-1">
              <span className="text-muted-foreground">{t('基础分流模式', 'Base Split Mode')}</span>
              <select
                className={fieldCls}
                value={detectRoutingPreset(policy)}
                onChange={(e) => {
                  const preset = e.target.value as RoutingPresetId
                  if (preset === 'custom') return
                  setPolicy(applyRoutingPreset(policy, preset, groups?.groups[0]?.name || 'proxy'))
                  setDraftDirty(true)
                }}
              >
                <option value="cn_direct">{t('中国直连 / 国外代理（绕过中国）', 'Bypass CN (CN Direct / Foreign Proxy)')}</option>
                <option value="gfw">{t('GFW 列表模式（仅 GFW 走代理）', 'GFW List Only')}</option>
                <option value="cn_proxy">{t('仅中国代理（回国模式）', 'CN Proxy Only')}</option>
                <option value="global_proxy">{t('全局代理', 'Global Proxy')}</option>
                <option value="global_direct">{t('全局直连', 'Global Direct')}</option>
                <option value="custom">{t('自定义', 'Custom')}</option>
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-muted-foreground">{t('默认兜底动作', 'Default Fallback')}</span>
              <select
                className={fieldCls}
                value={policy.defaultAction}
                onChange={(e) => { setPolicy({ ...policy, defaultAction: e.target.value as VisualPolicy['defaultAction'] }); setDraftDirty(true) }}
              >
                <option value="direct">{t('直连 (direct)', 'Direct')}</option>
                <option value="group">{t('代理组 (group)', 'Proxy Group')}</option>
                <option value="block">{t('阻断 (block)', 'Block')}</option>
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-muted-foreground">{t('默认代理组', 'Default Group')}</span>
              <select
                className={fieldCls}
                value={policy.defaultGroup}
                onChange={(e) => { setPolicy({ ...policy, defaultGroup: e.target.value }); setDraftDirty(true) }}
              >
                <option value="">--</option>
                {groups?.groups.map((g) => <option key={g.id} value={g.name}>{g.name}</option>)}
              </select>
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-muted-foreground">{t('不代理端口 (Skip)', 'Skip Proxy Ports')}</span>
              <input
                className={fieldCls}
                placeholder="udp:53,tcp:22"
                value={policy.skipProxyPorts}
                onChange={(e) => { setPolicy({ ...policy, skipProxyPorts: e.target.value }); setDraftDirty(true) }}
              />
            </label>
            <label className="flex flex-col gap-1">
              <span className="text-muted-foreground">{t('仅阻断代理端口 (如 udp:443)', 'Block Proxy-Only Ports')}</span>
              <input
                className={fieldCls}
                placeholder={t('留空表示不阻断，如 udp:443', 'Empty = none (e.g. udp:443)')}
                value={policy.blockProxyPorts}
                onChange={(e) => { setPolicy({ ...policy, blockProxyPorts: e.target.value }); setDraftDirty(true) }}
              />
            </label>
          </div>

          <div className={`flex flex-wrap items-center gap-2 text-xs ${!isVisualMode ? 'opacity-60 pointer-events-none' : ''}`}>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="h-7 px-2.5 text-xs"
              onClick={() => {
                setPolicy({
                  ...policy,
                  acls: [
                    ...policy.acls,
                    {
                      id: `acl-${Date.now()}`,
                      name: t('新设备策略', 'New Device ACL'),
                      enabled: true,
                      sources: [{ kind: 'mac', value: '' }],
                      protocol: 'any',
                      ports: '',
                      action: 'direct',
                      targetGroup: '',
                    },
                  ],
                })
                setDraftDirty(true)
              }}
            >
              + {t('添加设备规则', 'Add Device ACL')}
            </Button>
            <Button
              type="button"
              variant="secondary"
              size="sm"
              className="h-7 px-2.5 text-xs"
              onClick={() => refetchLan()}
            >
              {t('刷新 LAN 设备提示', 'Refresh LAN Devices')} ({lanDevices?.length || 0})
            </Button>
            {selectedRouting && (
              <Button
                type="button"
                variant="default"
                size="sm"
                className="h-7 px-2.5 text-xs"
                onClick={() => runAction(async () => {
                  const res = await m.savePolicyDraft.mutateAsync({
                    routingId: selectedRouting.id,
                    expectedRevision: draftState?.draftRevision,
                    policyJson: JSON.stringify(policy),
                  })
                  setDraftDirty(false)
                  setNotice(`${t('草稿已保存（未重载），修订', 'Draft saved (no reload), rev')} #${res.state.draftRevision}`)
                })}
              >
                {t('保存草稿', 'Save Draft')}
              </Button>
            )}
          </div>

          {lanDevices && lanDevices.length > 0 && (
            <div className={`text-xs text-muted-foreground flex flex-wrap items-center gap-1.5 ${!isVisualMode ? 'opacity-60 pointer-events-none' : ''}`}>
              <span>{t('发现的 LAN 设备', 'LAN Hints')}:</span>
              {lanDevices.slice(0, 8).map((d, i) => (
                <button
                  key={`${d.mac}-${d.ip}-${i}`}
                  type="button"
                  className="px-2 py-0.5 rounded-md border border-border bg-muted/60 text-foreground hover:bg-accent hover:text-accent-foreground transition-colors"
                  onClick={() => {
                    setPolicy({
                      ...policy,
                      acls: [
                        ...policy.acls,
                        {
                          id: `acl-${Date.now()}`,
                          name: d.hostname || d.ip || d.mac,
                          enabled: true,
                          sources: [{ kind: d.mac ? 'mac' : 'ip', value: d.mac || d.ip, label: d.hostname }],
                          protocol: 'any',
                          ports: '',
                          action: 'direct',
                          targetGroup: '',
                        },
                      ],
                    })
                    setDraftDirty(true)
                  }}
                >
                  {d.hostname || d.ip} ({d.mac || d.ip})
                </button>
              ))}
            </div>
          )}

          {/* ACL List */}
          <div className={`space-y-2 ${!isVisualMode ? 'opacity-60 pointer-events-none' : ''}`}>
            {policy.acls.map((acl, idx) => (
              <div key={acl.id} className="grid grid-cols-1 md:grid-cols-7 gap-2 items-center rounded-md border border-border bg-muted/20 p-2 text-xs">
                <div className="flex items-center gap-1.5">
                  <input
                    type="checkbox"
                    checked={acl.enabled}
                    onChange={(e) => {
                      const next = [...policy.acls]
                      next[idx] = { ...acl, enabled: e.target.checked }
                      setPolicy({ ...policy, acls: next })
                      setDraftDirty(true)
                    }}
                  />
                  <input
                    className={`w-full ${fieldCls}`}
                    value={acl.name}
                    onChange={(e) => {
                      const next = [...policy.acls]
                      next[idx] = { ...acl, name: e.target.value }
                      setPolicy({ ...policy, acls: next })
                      setDraftDirty(true)
                    }}
                  />
                </div>
                <select
                  className={fieldCls}
                  value={acl.sources[0]?.kind || 'mac'}
                  onChange={(e) => {
                    const next = [...policy.acls]
                    next[idx] = { ...acl, sources: [{ ...(acl.sources[0] || { value: '' }), kind: e.target.value as ACLSource['kind'] }] }
                    setPolicy({ ...policy, acls: next })
                    setDraftDirty(true)
                  }}
                >
                  <option value="mac">MAC</option>
                  <option value="ip">IP</option>
                  <option value="cidr">CIDR</option>
                  <option value="range">{t('IP 范围', 'IP Range')}</option>
                </select>
                <input
                  className={fieldCls}
                  placeholder="52:54:00:... / 192.0.2.10"
                  value={acl.sources[0]?.value || ''}
                  onChange={(e) => {
                    const next = [...policy.acls]
                    next[idx] = { ...acl, sources: [{ ...(acl.sources[0] || { kind: 'mac' }), value: e.target.value }] }
                    setPolicy({ ...policy, acls: next })
                    setDraftDirty(true)
                  }}
                />
                <select
                  className={fieldCls}
                  value={acl.action}
                  onChange={(e) => {
                    const next = [...policy.acls]
                    next[idx] = { ...acl, action: e.target.value as DeviceACLRule['action'] }
                    setPolicy({ ...policy, acls: next })
                    setDraftDirty(true)
                  }}
                >
                  <option value="direct">{t('直连', 'Direct')}</option>
                  <option value="group">{t('代理组', 'Group')}</option>
                  <option value="block">{t('阻断', 'Block')}</option>
                  <option value="inherit">{t('继承全局', 'Inherit')}</option>
                </select>
                <select
                  className={fieldCls}
                  value={acl.targetGroup}
                  disabled={acl.action !== 'group'}
                  onChange={(e) => {
                    const next = [...policy.acls]
                    next[idx] = { ...acl, targetGroup: e.target.value }
                    setPolicy({ ...policy, acls: next })
                    setDraftDirty(true)
                  }}
                >
                  <option value="">--</option>
                  {groups?.groups.map((g) => <option key={g.id} value={g.name}>{g.name}</option>)}
                </select>
                <input
                  className={fieldCls}
                  placeholder={t('端口如 80,443', 'Ports')}
                  value={acl.ports}
                  onChange={(e) => {
                    const next = [...policy.acls]
                    next[idx] = { ...acl, ports: e.target.value }
                    setPolicy({ ...policy, acls: next })
                    setDraftDirty(true)
                  }}
                />
                <div className="flex gap-1">
                  <Button type="button" variant="outline" size="sm" className="h-7 px-2 text-xs" disabled={idx === 0} onClick={() => {
                    const next = [...policy.acls];
                    [next[idx - 1], next[idx]] = [next[idx], next[idx - 1]]
                    setPolicy({ ...policy, acls: next }); setDraftDirty(true)
                  }}>↑</Button>
                  <Button type="button" variant="outline" size="sm" className="h-7 px-2 text-xs" disabled={idx === policy.acls.length - 1} onClick={() => {
                    const next = [...policy.acls];
                    [next[idx + 1], next[idx]] = [next[idx], next[idx + 1]]
                    setPolicy({ ...policy, acls: next }); setDraftDirty(true)
                  }}>↓</Button>
                  <Button type="button" variant="outline" size="sm" className="h-7 px-2 text-xs text-destructive hover:text-destructive" onClick={() => {
                    setPolicy({ ...policy, acls: policy.acls.filter((_, i) => i !== idx) }); setDraftDirty(true)
                  }}>×</Button>
                </div>
              </div>
            ))}
          </div>

          {/* Preview output */}
          {preview && (
            <div className="rounded-md border border-primary/30 bg-muted/40 p-3 text-xs space-y-2">
              <div className="flex flex-wrap justify-between gap-2 font-medium text-foreground">
                <span>{t('候选摘要', 'Digest')}: {preview.digest.slice(0, 16)}</span>
                <span>{t('容量预估', 'Capacity')}: {preview.capacity.estimatedMatchSets}/{preview.capacity.maxMatchSets} ({preview.capacity.ruleCount} {t('条规则', 'rules')})</span>
              </div>
              {preview.reloadWarning && <div className="text-amber-600 dark:text-amber-400">{preview.reloadWarning}</div>}
              {preview.diagnostics.length > 0 && (
                <ul className="list-disc pl-4 text-destructive">
                  {preview.diagnostics.map((d, i) => <li key={i}>[{d.scope}:{d.field}] {d.message}</li>)}
                </ul>
              )}
              <pre className="max-h-40 overflow-auto rounded-md border border-border bg-background/80 text-foreground p-2 text-[11px] font-mono">{preview.redactedDiff}</pre>
            </div>
          )}
        </div>
      )}

      {/* TAB 2: Rule Lists & DNS */}
      {tab === 'lists' && (
        <div className="space-y-4 text-xs">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
            <input className={fieldCls} placeholder={t('列表名称', 'List Name')} value={rlName} onChange={(e) => setRlName(e.target.value)} />
            <select className={fieldCls} value={rlKind} onChange={(e) => setRlKind(e.target.value)}>
              <option value="domain">domain</option>
              <option value="direct">direct</option>
              <option value="proxy">proxy</option>
              <option value="block">block</option>
              <option value="gfw">gfw</option>
              <option value="cn_domain">cn_domain</option>
              <option value="ipv4_cidr">ipv4_cidr</option>
              <option value="ipv6_cidr">ipv6_cidr</option>
            </select>
            <select className={fieldCls} value={rlSourceType} onChange={(e) => setRlSourceType(e.target.value)}>
              <option value="static">{t('静态粘贴文本', 'Static Text')}</option>
              <option value="remote">{t('远端 URL', 'Remote URL')}</option>
            </select>
          </div>
          {rlSourceType === 'remote' ? (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
              <input className={fieldCls} placeholder="https://..." value={rlUrl} onChange={(e) => setRlUrl(e.target.value)} />
              <select className={fieldCls} value={rlDownloadMode} onChange={(e) => setRlDownloadMode(e.target.value)}>
                <option value="auto">{t('自动下载', 'Auto')}</option>
                <option value="direct">{t('直连下载', 'Direct')}</option>
                <option value="group">{t('指定组下载', 'Specific Group')}</option>
              </select>
              <input className={fieldCls} placeholder={t('下载组名', 'Download Group')} value={rlDownloadGroup} onChange={(e) => setRlDownloadGroup(e.target.value)} />
            </div>
          ) : (
            <textarea className={`w-full font-mono ${fieldCls}`} rows={8} placeholder="example.com&#10;10.0.0.0/8&#10;||blocked.org" value={rlText} onChange={(e) => setRlText(e.target.value)} />
          )}
          <div className="flex gap-2 items-center">
            <Button
              type="button"
              variant="default"
              size="sm"
              className="h-7 px-3 text-xs"
              onClick={() => runAction(async () => {
                await m.saveRuleList.mutateAsync({
                  id: rlEditingId || undefined,
                  expectedRevision: rlEditingId ? rlEditingRev : undefined,
                  name: rlName, kind: rlKind, sourceType: rlSourceType, url: rlUrl,
                  downloadMode: rlDownloadMode, downloadGroupName: rlDownloadGroup,
                  cronEnable: false, rawText: rlText,
                })
                setRlEditingId(null); setRlEditingRev(undefined)
                setRlName(''); setRlText(''); setRlUrl('')
                setNotice(t('规则列表已保存（别忘了点顶部「完整应用」生效）', 'Rule list saved (click Apply Revision to activate)'))
              })}
            >
              {rlEditingId ? t('保存修改', 'Update Rule List') : t('新建规则列表', 'Create Rule List')}
            </Button>
            {rlEditingId && (
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-7 px-3 text-xs"
                onClick={() => {
                  setRlEditingId(null); setRlEditingRev(undefined)
                  setRlName(''); setRlKind('domain'); setRlSourceType('static')
                  setRlUrl(''); setRlDownloadMode('auto'); setRlDownloadGroup(''); setRlText('')
                }}
              >
                {t('取消编辑', 'Cancel Edit')}
              </Button>
            )}
          </div>

          <div className="space-y-2">
            {ruleLists?.map((rl) => (
              <div key={rl.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/20 p-2.5">
                <div>
                  <span className="font-medium text-foreground">{rl.name}</span> <span className="text-muted-foreground">({rl.kind} · {rl.sourceType}) — {rl.lastEntryCount} {t('条', 'entries')} (+{rl.lastAddedCount}/-{rl.lastRemovedCount}) · {rl.lastStatus}</span>
                  {rl.lastError && <div className="text-destructive">{rl.lastError}</div>}
                </div>
                <div className="flex gap-1.5">
                  <Button type="button" variant="outline" size="sm" className="h-7 px-2.5 text-xs" onClick={() => {
                    setRlEditingId(rl.id)
                    setRlEditingRev(rl.revision)
                    setRlName(rl.name)
                    setRlKind(rl.kind)
                    setRlSourceType(rl.sourceType)
                    setRlUrl(rl.url || '')
                    setRlDownloadMode(rl.downloadMode || 'auto')
                    setRlDownloadGroup(rl.downloadGroupName || '')
                    setRlText(rl.rawText || '')
                  }}>{t('查看 / 编辑', 'View / Edit')}</Button>
                  {rl.sourceType === 'remote' && (
                    <Button type="button" variant="outline" size="sm" className="h-7 px-2.5 text-xs" onClick={() => runAction(async () => {
                      const r = await m.refreshRuleList.mutateAsync(rl.id)
                      setNotice(`${rl.name}: +${r.addedCount}/-${r.removedCount}`)
                    })}>{t('刷新', 'Refresh')}</Button>
                  )}
                  <Button type="button" variant="outline" size="sm" className="h-7 px-2.5 text-xs text-destructive hover:text-destructive" onClick={() => runAction(async () => {
                    await m.deleteRuleList.mutateAsync(rl.id)
                    setNotice(t('已删除规则列表', 'Rule list deleted'))
                  })}>{t('删除', 'Delete')}</Button>
                </div>
              </div>
            ))}
          </div>

          {selectedDns && (
            <div className="border-t border-border pt-3">
              <div className="font-medium mb-1 text-muted-foreground">{t('当前 DNS 绑定的域名列表', 'DNS Domain List Bindings')}: {selectedDns.name} ({dnsBindings?.length || 0})</div>
            </div>
          )}
        </div>
      )}

      {/* TAB 3: Enhanced Subscriptions */}
      {tab === 'subscriptions' && (
        <div className="space-y-3 text-xs">
          <div className="flex items-center gap-2">
            <span className="text-muted-foreground">{t('选择订阅', 'Subscription')}:</span>
            <select className={fieldCls} value={selectedSubId} onChange={(e) => setSelectedSubId(e.target.value)}>
              {subs?.subscriptions.map((s) => <option key={s.id} value={s.id}>{s.tag || s.link}</option>)}
            </select>
          </div>
          {selectedSubId && (
            <>
              <div className="grid grid-cols-1 md:grid-cols-3 gap-2">
                <select className={fieldCls} value={subFormat} onChange={(e) => setSubFormat(e.target.value)}>
                  <option value="auto">{t('自动识别格式', 'Auto Format')}</option>
                  <option value="clash_yaml">Clash YAML</option>
                  <option value="sip008">SIP008</option>
                  <option value="base64">Base64 / Links</option>
                </select>
                <input className={fieldCls} placeholder="User-Agent" value={subUA} onChange={(e) => setSubUA(e.target.value)} />
                <select className={fieldCls} value={subDlMode} onChange={(e) => setSubDlMode(e.target.value)}>
                  <option value="auto">{t('自动下载', 'Auto')}</option>
                  <option value="direct">{t('直连下载', 'Direct')}</option>
                  <option value="group">{t('指定组下载', 'Specific Group')}</option>
                </select>
                <input className={fieldCls} placeholder={t('指定下载组名', 'Download Group Name')} value={subDlGroup} onChange={(e) => setSubDlGroup(e.target.value)} />
                <input className={fieldCls} placeholder={t('保留关键词 (逗号分隔)', 'Include Keywords')} value={subInc} onChange={(e) => setSubInc(e.target.value)} />
                <input className={fieldCls} placeholder={t('排除关键词 (逗号分隔)', 'Exclude Keywords')} value={subExc} onChange={(e) => setSubExc(e.target.value)} />
              </div>
              <div className="flex flex-wrap gap-2 items-center">
                <select className={fieldCls} value={subPriority} onChange={(e) => setSubPriority(e.target.value)}>
                  <option value="exclude_first">{t('冲突时排除优先', 'Exclude First')}</option>
                  <option value="include_first">{t('冲突时保留优先', 'Include First')}</option>
                </select>
                <Button type="button" variant="default" size="sm" className="h-7 px-3 text-xs" onClick={() => runAction(async () => {
                  await m.updateSubscriptionEnhanced.mutateAsync({
                    id: selectedSubId,
                    input: { format: subFormat, userAgent: subUA, downloadMode: subDlMode, downloadGroupName: subDlGroup, includeKeywords: subInc, excludeKeywords: subExc, filterPriority: subPriority },
                  })
                  setNotice(t('订阅高级设置已保存', 'Subscription settings saved'))
                })}>{t('保存设置', 'Save Settings')}</Button>
                <Button type="button" variant="outline" size="sm" className="h-7 px-3 text-xs" onClick={() => runAction(async () => {
                  const r = await m.refreshSubscriptionEnhanced.mutateAsync(selectedSubId)
                  setNotice(`+${r.addedCount} ~${r.updatedCount} -${r.removedCount} rebind:${r.rebindCount} filtered:${r.filteredCount} errors:${r.errors.length}`)
                })}>{t('稳定 ID 刷新', 'Stable Refresh')}</Button>
              </div>
              {subEnhanced && (
                <div className="text-muted-foreground">
                  {t('上次差异', 'Last Diff')}: {subEnhanced.lastDiffSummary || '-'} · {t('流量', 'Traffic')}: ↑{subEnhanced.trafficUpload} ↓{subEnhanced.trafficDownload} / {subEnhanced.trafficTotal}
                </div>
              )}
            </>
          )}
        </div>
      )}

      {/* TAB 4: Proxy Chains */}
      {tab === 'chains' && (
        <div className="space-y-3 text-xs">
          <div className="grid grid-cols-1 md:grid-cols-4 gap-2 items-center">
            <input className={fieldCls} placeholder={t('代理链名称', 'Chain Name')} value={chainName} onChange={(e) => setChainName(e.target.value)} />
            <select className={fieldCls} value={chainHop1} onChange={(e) => setChainHop1(e.target.value)}>
              <option value="">{t('前置跳 (Hop 1)', 'Entry Hop 1')}</option>
              {nodes?.nodes.edges.map((n) => <option key={n.id} value={n.id}>{n.tag || n.name}</option>)}
            </select>
            <select className={fieldCls} value={chainHop2} onChange={(e) => setChainHop2(e.target.value)}>
              <option value="">{t('落地跳 (Hop 2)', 'Exit Hop 2')}</option>
              {nodes?.nodes.edges.map((n) => <option key={n.id} value={n.id}>{n.tag || n.name}</option>)}
            </select>
            <Button type="button" variant="default" size="sm" className="h-8 px-3 text-xs" onClick={() => runAction(async () => {
              await m.saveProxyChain.mutateAsync({ name: chainName, memberNodeIds: [chainHop1, chainHop2] })
              setChainName('')
              setNotice(t('代理链已保存', 'Proxy chain saved'))
            })}>{t('创建代理链', 'Save Chain')}</Button>
          </div>
          <div className="space-y-2">
            {proxyChains?.map((ch) => (
              <div key={ch.id} className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/20 p-2.5">
                <div>
                  <span className="font-medium text-foreground">{ch.name}</span>: <span className="text-muted-foreground">{t('路由器', 'Router')} → {ch.memberNames.join(' → ')} → {t('目标', 'Target')}</span>
                  <span className={`ml-2 px-1.5 py-0.5 rounded-md border text-[11px] ${ch.parseable ? 'border-emerald-500/30 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400' : 'border-destructive/30 bg-destructive/15 text-destructive'}`}>
                    {ch.parseable ? t('可解析', 'Parseable') : ch.parseError}
                  </span>
                </div>
                <div className="flex gap-1.5 items-center">
                  <select className={fieldCls} value={chainAttachGroup} onChange={(e) => setChainAttachGroup(e.target.value)}>
                    <option value="">{t('加入分组...', 'Attach to group...')}</option>
                    {groups?.groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
                  </select>
                  <Button type="button" variant="outline" size="sm" className="h-7 px-2.5 text-xs" disabled={!chainAttachGroup} onClick={() => runAction(async () => {
                    await m.groupAddChains.mutateAsync({ id: chainAttachGroup, chainIDs: [ch.id] })
                    setNotice(t('已将代理链加入分组', 'Chain attached to group'))
                  })}>{t('加入', 'Add')}</Button>
                  <Button type="button" variant="outline" size="sm" className="h-7 px-2.5 text-xs text-destructive hover:text-destructive" onClick={() => runAction(async () => {
                    await m.deleteProxyChain.mutateAsync(ch.id)
                  })}>{t('删除', 'Delete')}</Button>
                </div>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* TAB 5: Group Failover */}
      {tab === 'failover' && (
        <div className="space-y-3 text-xs">
          <div className="flex flex-wrap items-center gap-3">
            <select className={fieldCls} value={selectedGroupId} onChange={(e) => setSelectedGroupId(e.target.value)}>
              {groups?.groups.map((g) => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
            <label className="flex items-center gap-1.5 text-foreground"><input type="checkbox" checked={foEnabled} onChange={(e) => setFoEnabled(e.target.checked)} /> {t('启用有序主备', 'Enable Ordered Failover')}</label>
            <label className="flex items-center gap-1.5 text-foreground"><input type="checkbox" checked={foAutoFailback} onChange={(e) => setFoAutoFailback(e.target.checked)} /> {t('主节点恢复自动回切', 'Auto Failback')}</label>
          </div>
          <div className="grid grid-cols-1 md:grid-cols-4 gap-2">
            <input className={fieldCls} placeholder="Primary (e.g. node:1)" value={foPrimary} onChange={(e) => setFoPrimary(e.target.value)} />
            <input className={fieldCls} placeholder="Backups (node:2,node:3)" value={foBackups} onChange={(e) => setFoBackups(e.target.value)} />
            <input className={fieldCls} placeholder="Probe URL" value={foProbeUrl} onChange={(e) => setFoProbeUrl(e.target.value)} />
            <input className={fieldCls} placeholder="Probe DNS" value={foProbeDns} onChange={(e) => setFoProbeDns(e.target.value)} />
          </div>
          <div className="flex gap-2">
            <Button type="button" variant="default" size="sm" className="h-7 px-3 text-xs" onClick={() => runAction(async () => {
              await m.saveGroupFailover.mutateAsync({
                groupId: selectedGroupId, enabled: foEnabled, primaryMemberKey: foPrimary,
                backupMemberKeys: foBackups.split(',').map((s) => s.trim()).filter(Boolean),
                autoFailback: foAutoFailback, probeUrl: foProbeUrl, probeDns: foProbeDns,
              })
              setNotice(t('主备配置已保存', 'Failover config saved'))
            })}>{t('保存主备配置', 'Save Failover')}</Button>
            <Button type="button" variant="outline" size="sm" className="h-7 px-3 text-xs" onClick={() => runAction(async () => {
              await m.probeGroupFailover.mutateAsync(selectedGroupId)
              setNotice(t('已完成一次主备探测', 'Failover probe completed'))
            })}>{t('立即探测', 'Probe Now')}</Button>
          </div>
          {failoverStatus && (
            <div className="rounded-md border border-border bg-muted/30 p-2.5 space-y-1 text-foreground">
              <div>TCP: {failoverStatus.tcp.activeMemberKey || '-'} (fail:{failoverStatus.tcp.consecFailures}, noBackup:{String(failoverStatus.tcp.noHealthyBackup)}) {failoverStatus.tcp.lastSwitchReason}</div>
              <div>UDP: {failoverStatus.udp.activeMemberKey || '-'} (fail:{failoverStatus.udp.consecFailures}, noBackup:{String(failoverStatus.udp.noHealthyBackup)}) {failoverStatus.udp.lastSwitchReason}</div>
            </div>
          )}
        </div>
      )}

      {/* TAB 6: Diagnostics & Active Route Trace */}
      {tab === 'diagnostics' && (
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4 text-xs">
          <div className="rounded-md border border-border bg-muted/20 p-3 space-y-2">
            <div className="font-medium text-foreground">{t('节点 / 代理链路径探测', 'Node / Chain Path Probe')}</div>
            <div className="grid grid-cols-2 gap-2">
              <select className={fieldCls} value={diagTargetKind} onChange={(e) => setDiagTargetKind(e.target.value as 'node' | 'chain')}>
                <option value="node">{t('节点', 'Node')}</option>
                <option value="chain">{t('代理链', 'Proxy Chain')}</option>
              </select>
              <select className={fieldCls} value={diagTargetId} onChange={(e) => setDiagTargetId(e.target.value)}>
                <option value="">--</option>
                {diagTargetKind === 'node'
                  ? nodes?.nodes.edges.map((n) => <option key={n.id} value={n.id}>{n.tag || n.name}</option>)
                  : proxyChains?.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
              <select className={fieldCls} value={diagProbeType} onChange={(e) => setDiagProbeType(e.target.value)}>
                <option value="url">HTTP URL (generate_204)</option>
                <option value="udp_dns">UDP DNS</option>
                <option value="tcp">TCP Connect</option>
                <option value="icmp">ICMP Ping</option>
              </select>
              <input className={fieldCls} value={diagDest} onChange={(e) => setDiagDest(e.target.value)} />
            </div>
            <Button type="button" variant="default" size="sm" className="h-7 px-3 text-xs" disabled={!diagTargetId} onClick={() => runAction(async () => {
              const r = await m.testNodePath.mutateAsync({ targetKind: diagTargetKind, targetId: diagTargetId, probeType: diagProbeType, destination: diagDest })
              setProbeResult(r)
            })}>{t('运行路径探测', 'Run Probe')}</Button>
            {probeResult && (
              <div className="rounded-md border border-border bg-background/80 p-2 space-y-1 text-foreground">
                <div>{probeResult.ok ? 'OK' : 'FAIL'} · {probeResult.latencyMs}ms · stage={probeResult.stage} · {probeResult.message}</div>
                {probeResult.hopResults.map((h, i) => (
                  <div key={i} className="pl-2 text-muted-foreground">Hop {i + 1} ({h.targetName}): {h.ok ? 'OK' : 'FAIL'} {h.latencyMs}ms ({h.message})</div>
                ))}
              </div>
            )}
          </div>

          <div className="rounded-md border border-border bg-muted/20 p-3 space-y-2">
            <div className="font-medium text-foreground">{t('活动控制面规则追踪 (与草稿预览独立)', 'Active Control-Plane Route Trace')}</div>
            <div className="grid grid-cols-2 gap-2">
              <select className={fieldCls} value={traceNet} onChange={(e) => setTraceNet(e.target.value)}>
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
              <input className={fieldCls} placeholder={t('目的端口', 'Dst Port')} value={traceDstPort} onChange={(e) => setTraceDstPort(e.target.value)} />
              <input className={fieldCls} placeholder={t('域名 (可选)', 'Domain')} value={traceDomain} onChange={(e) => setTraceDomain(e.target.value)} />
              <input className={fieldCls} placeholder={t('目的 IP (可选)', 'Dst IP')} value={traceDstIp} onChange={(e) => setTraceDstIp(e.target.value)} />
              <input className={`${fieldCls} col-span-2`} placeholder={t('源 IP (可选；注意 MAC 条件会提示缺少 src_mac)', 'Src IP (optional)')} value={traceSrcIp} onChange={(e) => setTraceSrcIp(e.target.value)} />
            </div>
            <Button type="button" variant="default" size="sm" className="h-7 px-3 text-xs" onClick={() => runAction(async () => {
              const r = await m.traceActiveRoute.mutateAsync({
                network: traceNet,
                domain: traceDomain || undefined,
                dstIp: traceDstIp || undefined,
                dstPort: Number(traceDstPort) || 443,
                srcIp: traceSrcIp || undefined,
              })
              setTraceResult(r)
            })}>{t('查询活动规则', 'Trace Active Route')}</Button>
            {traceResult && (
              <div className="rounded-md border border-border bg-background/80 p-2 space-y-1 text-foreground">
                <div>available={String(traceResult.available)} · gen={traceResult.generation} · decision={traceResult.decision} · outbound={traceResult.outbound || '-'}</div>
                {traceResult.unavailableReason && <div className="text-amber-600 dark:text-amber-400">{traceResult.unavailableReason}</div>}
                {traceResult.missingInputs.length > 0 && (
                  <div className="text-amber-600 dark:text-amber-400">{t('缺少输入条件', 'Missing Inputs')}: {traceResult.missingInputs.join(', ')}</div>
                )}
              </div>
            )}
          </div>
        </div>
      )}
      </CardContent>
      )}
    </Card>
  )
}
