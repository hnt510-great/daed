import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useGQLQueryClient } from '~/contexts'
import { QUERY_KEY_GENERAL, QUERY_KEY_GROUP, QUERY_KEY_NODE, QUERY_KEY_ROUTING, QUERY_KEY_SUBSCRIPTION } from '~/constants'

export const QK_LAN_DEVICES = ['lanDevices']
export const QK_RULE_LISTS = ['ruleLists']
export const QK_POLICY_DRAFT = ['policyDraft']
export const QK_POLICY_PREVIEW = ['policyPreview']
export const QK_SNAPSHOTS = ['configSnapshots']
export const QK_PROXY_CHAINS = ['proxyChains']
export const QK_GROUP_FAILOVER = ['groupFailover']
export const QK_SUB_ENHANCED = ['subscriptionEnhanced']
export const QK_DNS_BINDINGS = ['dnsListBindings']
export const QK_APPLY_STATE = ['systemApplyState']

export interface LanDeviceHint {
  hostname: string
  ip: string
  mac: string
  interfaceName: string
  source: string
  online: boolean
}

export interface DiagnosticItem {
  scope: string
  field: string
  level: string
  message: string
}

export interface RuleListItem {
  id: string
  name: string
  kind: string
  targetGroup: string
  sourceType: string
  url: string
  downloadMode: string
  downloadGroupName: string
  userAgent: string
  cronExp: string
  cronEnable: boolean
  rawText: string
  lastDigest: string
  lastEntryCount: number
  lastAddedCount: number
  lastRemovedCount: number
  lastStatus: string
  lastError: string
  lastUpdatedAt?: string | null
  revision: number
  version: number
  references: { objectType: string; objectId: string; objectName: string }[]
}

export interface PolicyDraftState {
  routingId: string
  name: string
  editMode: string
  draftRevision: number
  policyJson: string
  generatedRouting: string
}

export interface PolicyPreview {
  digest: string
  draftRevision: number
  hasErrors: boolean
  hasEffectiveChanges: boolean
  reloadWarning: string
  expandedRules: string[]
  redactedDiff: string
  dependencies: { key: string; value: string }[]
  diagnostics: DiagnosticItem[]
  capacity: { estimatedMatchSets: number; maxMatchSets: number; ruleCount: number; exceeded: boolean }
}

export interface ConfigSnapshotItem {
  id: string
  createdAt: string
  digest: string
  summary: string
  active: boolean
}

export interface ProxyChainItem {
  id: string
  name: string
  tag?: string | null
  memberNodeIds: string[]
  memberNames: string[]
  parseable: boolean
  parseError: string
  needsRebind: boolean
  rebindReason: string
  version: number
}

export interface GroupFailoverStatus {
  groupId: string
  groupName: string
  enabled: boolean
  primaryMemberKey: string
  backupMemberKeys: string[]
  probeIntervalSec: number
  probeTimeoutSec: number
  failThreshold: number
  recoverThreshold: number
  autoFailback: boolean
  probeUrl: string
  probeDns: string
  revision: number
  generation: number
  tcp: {
    protocol: string
    activeMemberKey: string
    activeIndex: number
    consecFailures: number
    primaryConsecOK: number
    noHealthyBackup: boolean
    lastSwitchReason: string
    lastSwitchAt?: string | null
  }
  udp: {
    protocol: string
    activeMemberKey: string
    activeIndex: number
    consecFailures: number
    primaryConsecOK: number
    noHealthyBackup: boolean
    lastSwitchReason: string
    lastSwitchAt?: string | null
  }
  members: {
    memberKey: string
    index: number
    tcpAlive: boolean
    udpAlive: boolean
    tcpLatMs: number
    udpLatMs: number
    lastError: string
  }[]
  lastProbeAt?: string | null
}

export interface SubscriptionEnhancedItem {
  id: string
  format: string
  userAgent: string
  downloadMode: string
  downloadGroupName: string
  includeKeywords: string
  excludeKeywords: string
  filterPriority: string
  trafficUpload: string
  trafficDownload: string
  trafficTotal: string
  expireAt?: string | null
  lastDiffSummary: string
  lastError: string
  revision: number
}

export interface SystemApplyState {
  running: boolean
  generation: number
  activeDigest: string
  activeSnapshotId?: string | null
  lastApplyStatus: string
  lastApplyError: string
  autoApplyEnabled: boolean
}

export interface NodePathProbeResult {
  targetKind: string
  targetId: string
  targetName: string
  probeType: string
  destination: string
  protocol: string
  parseable: boolean
  ok: boolean
  latencyMs: number
  stage: string
  message: string
  testedAt: string
  hopResults: NodePathProbeResult[]
}

export interface TraceRouteResult {
  available: boolean
  unavailableReason: string
  generation: number
  activeDigest: string
  decision: string
  outbound: string
  missingInputs: string[]
  rules: { ruleId: string; expression: string; result: string; missingInputs: string[] }[]
}

export function useSystemApplyStateQuery() {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: QK_APPLY_STATE,
    queryFn: async () => {
      const res = await gqlClient.request<{ systemApplyState: SystemApplyState }>(`
        query SystemApplyState {
          systemApplyState {
            running generation activeDigest activeSnapshotId lastApplyStatus lastApplyError autoApplyEnabled
          }
        }
      `)
      return res.systemApplyState
    },
    refetchInterval: 5000,
  })
}

export function useLanDevicesQuery() {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: QK_LAN_DEVICES,
    queryFn: async () => {
      const res = await gqlClient.request<{ lanDevices: LanDeviceHint[] }>(`
        query LanDevices {
          lanDevices { hostname ip mac interfaceName source online }
        }
      `)
      return res.lanDevices
    },
  })
}

export function useRuleListsQuery() {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: QK_RULE_LISTS,
    queryFn: async () => {
      const res = await gqlClient.request<{ ruleLists: RuleListItem[] }>(`
        query RuleLists {
          ruleLists {
            id name kind targetGroup sourceType url downloadMode downloadGroupName userAgent
            cronExp cronEnable rawText lastDigest lastEntryCount lastAddedCount lastRemovedCount
            lastStatus lastError lastUpdatedAt revision version
            references { objectType objectId objectName }
          }
        }
      `)
      return res.ruleLists
    },
  })
}

export function usePolicyDraftQuery(routingId?: string) {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: [...QK_POLICY_DRAFT, routingId],
    enabled: !!routingId,
    queryFn: async () => {
      const res = await gqlClient.request<{ policyDraft: PolicyDraftState }>(`
        query PolicyDraft($routingId: ID!) {
          policyDraft(routingId: $routingId) {
            routingId name editMode draftRevision policyJson generatedRouting
          }
        }
      `, { routingId })
      return res.policyDraft
    },
  })
}

export function useConfigSnapshotsQuery() {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: QK_SNAPSHOTS,
    queryFn: async () => {
      const res = await gqlClient.request<{ configSnapshots: ConfigSnapshotItem[] }>(`
        query ConfigSnapshots {
          configSnapshots { id createdAt digest summary active }
        }
      `)
      return res.configSnapshots
    },
  })
}

export function useProxyChainsQuery() {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: QK_PROXY_CHAINS,
    queryFn: async () => {
      const res = await gqlClient.request<{ proxyChains: ProxyChainItem[] }>(`
        query ProxyChains {
          proxyChains { id name tag memberNodeIds memberNames parseable parseError needsRebind rebindReason version }
        }
      `)
      return res.proxyChains
    },
  })
}

export function useGroupFailoverQuery(groupId?: string) {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: [...QK_GROUP_FAILOVER, groupId],
    enabled: !!groupId,
    queryFn: async () => {
      const res = await gqlClient.request<{ groupFailover: GroupFailoverStatus }>(`
        query GroupFailover($groupId: ID!) {
          groupFailover(groupId: $groupId) {
            groupId groupName enabled primaryMemberKey backupMemberKeys
            probeIntervalSec probeTimeoutSec failThreshold recoverThreshold
            autoFailback probeUrl probeDns revision generation lastProbeAt
            tcp { protocol activeMemberKey activeIndex consecFailures primaryConsecOK noHealthyBackup lastSwitchReason lastSwitchAt }
            udp { protocol activeMemberKey activeIndex consecFailures primaryConsecOK noHealthyBackup lastSwitchReason lastSwitchAt }
            members { memberKey index tcpAlive udpAlive tcpLatMs udpLatMs lastError }
          }
        }
      `, { groupId })
      return res.groupFailover
    },
  })
}

export function useSubscriptionEnhancedQuery(id?: string) {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: [...QK_SUB_ENHANCED, id],
    enabled: !!id,
    queryFn: async () => {
      const res = await gqlClient.request<{ subscriptionEnhanced: SubscriptionEnhancedItem }>(`
        query SubscriptionEnhanced($id: ID!) {
          subscriptionEnhanced(id: $id) {
            id format userAgent downloadMode downloadGroupName
            includeKeywords excludeKeywords filterPriority
            trafficUpload trafficDownload trafficTotal expireAt
            lastDiffSummary lastError revision
          }
        }
      `, { id })
      return res.subscriptionEnhanced
    },
  })
}

export function useDnsListBindingsQuery(dnsId?: string) {
  const gqlClient = useGQLQueryClient()
  return useQuery({
    queryKey: [...QK_DNS_BINDINGS, dnsId],
    enabled: !!dnsId,
    queryFn: async () => {
      const res = await gqlClient.request<{ dnsListBindings: { listId: string; upstream: string; qtype: string }[] }>(`
        query DnsListBindings($dnsId: ID!) {
          dnsListBindings(dnsId: $dnsId) { listId upstream qtype }
        }
      `, { dnsId })
      return res.dnsListBindings
    },
  })
}

export function useClientFeatureMutations() {
  const gqlClient = useGQLQueryClient()
  const qc = useQueryClient()

  const setRoutingEditMode = useMutation({
    mutationFn: async (vars: { routingId: string; editMode: 'raw' | 'visual' }) => {
      const res = await gqlClient.request<{ setRoutingEditMode: PolicyDraftState }>(`
        mutation SetRoutingEditMode($routingId: ID!, $editMode: String!) {
          setRoutingEditMode(routingId: $routingId, editMode: $editMode) {
            routingId name editMode draftRevision policyJson generatedRouting
          }
        }
      `, vars)
      return res.setRoutingEditMode
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: QK_POLICY_DRAFT })
      qc.invalidateQueries({ queryKey: QUERY_KEY_ROUTING })
      qc.invalidateQueries({ queryKey: QUERY_KEY_GENERAL })
    },
  })

  const savePolicyDraft = useMutation({
    mutationFn: async (vars: { routingId: string; expectedRevision?: number; policyJson: string }) => {
      const res = await gqlClient.request<{ savePolicyDraft: { state: PolicyDraftState; diagnostics: DiagnosticItem[] } }>(`
        mutation SavePolicyDraft($routingId: ID!, $expectedRevision: Int, $policyJson: String!) {
          savePolicyDraft(routingId: $routingId, expectedRevision: $expectedRevision, policyJson: $policyJson) {
            state { routingId name editMode draftRevision policyJson generatedRouting }
            diagnostics { scope field level message }
          }
        }
      `, vars)
      return res.savePolicyDraft
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: QK_POLICY_DRAFT })
      qc.invalidateQueries({ queryKey: QUERY_KEY_ROUTING })
      qc.invalidateQueries({ queryKey: QUERY_KEY_GENERAL })
    },
  })

  const previewPolicy = useMutation({
    mutationFn: async (vars?: { configId?: string; dnsId?: string; routingId?: string }) => {
      const res = await gqlClient.request<{ previewPolicy: PolicyPreview }>(`
        query PreviewPolicy($configId: ID, $dnsId: ID, $routingId: ID) {
          previewPolicy(configId: $configId, dnsId: $dnsId, routingId: $routingId) {
            digest draftRevision hasErrors hasEffectiveChanges reloadWarning
            expandedRules redactedDiff
            dependencies { key value }
            diagnostics { scope field level message }
            capacity { estimatedMatchSets maxMatchSets ruleCount exceeded }
          }
        }
      `, vars || {})
      return res.previewPolicy
    },
  })

  const applyPolicyRevision = useMutation({
    mutationFn: async (vars: { expectedDraftRevision?: number; expectedDigest?: string; configId?: string; dnsId?: string; routingId?: string }) => {
      const res = await gqlClient.request<{ applyPolicyRevision: { success: boolean; running: boolean; rolledBack: boolean; snapshotId?: string; digest: string; generation: number; status: string; message: string; diagnostics: DiagnosticItem[] } }>(`
        mutation ApplyPolicyRevision($expectedDraftRevision: Int, $expectedDigest: String, $configId: ID, $dnsId: ID, $routingId: ID) {
          applyPolicyRevision(expectedDraftRevision: $expectedDraftRevision, expectedDigest: $expectedDigest, configId: $configId, dnsId: $dnsId, routingId: $routingId) {
            success running rolledBack snapshotId digest generation status message
            diagnostics { scope field level message }
          }
        }
      `, vars)
      return res.applyPolicyRevision
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: QK_APPLY_STATE })
      qc.invalidateQueries({ queryKey: QK_SNAPSHOTS })
      qc.invalidateQueries({ queryKey: QUERY_KEY_GENERAL })
    },
  })

  const rollbackPolicy = useMutation({
    mutationFn: async (snapshotId: string) => {
      const res = await gqlClient.request<{ rollbackPolicy: { success: boolean; status: string; message: string } }>(`
        mutation RollbackPolicy($snapshotId: ID!) {
          rollbackPolicy(snapshotId: $snapshotId) { success status message }
        }
      `, { snapshotId })
      return res.rollbackPolicy
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: QK_APPLY_STATE })
      qc.invalidateQueries({ queryKey: QK_SNAPSHOTS })
      qc.invalidateQueries({ queryKey: QUERY_KEY_GENERAL })
    },
  })

  const setConfigAutoApply = useMutation({
    mutationFn: async (vars: { configId: string; autoApply: boolean }) => {
      await gqlClient.request(`
        mutation SetConfigAutoApply($configId: ID!, $autoApply: Boolean!) {
          setConfigAutoApply(configId: $configId, autoApply: $autoApply)
        }
      `, vars)
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_APPLY_STATE }),
  })

  const saveRuleList = useMutation({
    mutationFn: async (input: Record<string, unknown>) => {
      const res = await gqlClient.request<{ saveRuleList: { ruleList: RuleListItem; diagnostics: DiagnosticItem[] } }>(`
        mutation SaveRuleList($input: SaveRuleListInput!) {
          saveRuleList(input: $input) {
            ruleList { id name kind lastEntryCount lastStatus lastError revision version }
            diagnostics { scope field level message }
          }
        }
      `, { input })
      return res.saveRuleList
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_RULE_LISTS }),
  })

  const deleteRuleList = useMutation({
    mutationFn: async (id: string) => {
      await gqlClient.request(`mutation DeleteRuleList($id: ID!) { deleteRuleList(id: $id) }`, { id })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_RULE_LISTS }),
  })

  const refreshRuleList = useMutation({
    mutationFn: async (id: string) => {
      const res = await gqlClient.request<{ refreshRuleList: { changed: boolean; addedCount: number; removedCount: number; diagnostics: DiagnosticItem[] } }>(`
        mutation RefreshRuleList($id: ID!) {
          refreshRuleList(id: $id) { changed addedCount removedCount diagnostics { scope field level message } }
        }
      `, { id })
      return res.refreshRuleList
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_RULE_LISTS }),
  })

  const setDnsListBindings = useMutation({
    mutationFn: async (vars: { dnsId: string; bindings: { listId: string; upstream: string; qtype?: string }[] }) => {
      await gqlClient.request(`
        mutation SetDnsListBindings($dnsId: ID!, $bindings: [DnsListBindingInput!]!) {
          setDnsListBindings(dnsId: $dnsId, bindings: $bindings)
        }
      `, vars)
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_DNS_BINDINGS }),
  })

  const updateSubscriptionEnhanced = useMutation({
    mutationFn: async (vars: { id: string; input: Record<string, unknown> }) => {
      const res = await gqlClient.request<{ updateSubscriptionEnhanced: SubscriptionEnhancedItem }>(`
        mutation UpdateSubscriptionEnhanced($id: ID!, $input: SubscriptionEnhancedInput!) {
          updateSubscriptionEnhanced(id: $id, input: $input) {
            id format userAgent downloadMode downloadGroupName includeKeywords excludeKeywords filterPriority revision
          }
        }
      `, vars)
      return res.updateSubscriptionEnhanced
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: QK_SUB_ENHANCED })
      qc.invalidateQueries({ queryKey: QUERY_KEY_SUBSCRIPTION })
    },
  })

  const refreshSubscriptionEnhanced = useMutation({
    mutationFn: async (id: string) => {
      const res = await gqlClient.request<{ refreshSubscriptionEnhanced: { changed: boolean; addedCount: number; updatedCount: number; removedCount: number; rebindCount: number; filteredCount: number; errors: { index: number; protocol: string; param: string; reason: string }[] } }>(`
        mutation RefreshSubscriptionEnhanced($id: ID!) {
          refreshSubscriptionEnhanced(id: $id) {
            changed addedCount updatedCount removedCount rebindCount filteredCount
            errors { index protocol param reason }
          }
        }
      `, { id })
      return res.refreshSubscriptionEnhanced
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: QK_SUB_ENHANCED })
      qc.invalidateQueries({ queryKey: QUERY_KEY_SUBSCRIPTION })
      qc.invalidateQueries({ queryKey: QUERY_KEY_NODE })
      qc.invalidateQueries({ queryKey: QUERY_KEY_GROUP })
    },
  })

  const saveProxyChain = useMutation({
    mutationFn: async (input: { id?: string; name: string; tag?: string; memberNodeIds: string[] }) => {
      const res = await gqlClient.request<{ saveProxyChain: ProxyChainItem }>(`
        mutation SaveProxyChain($input: SaveProxyChainInput!) {
          saveProxyChain(input: $input) { id name memberNodeIds memberNames parseable parseError needsRebind rebindReason version }
        }
      `, { input })
      return res.saveProxyChain
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_PROXY_CHAINS }),
  })

  const deleteProxyChain = useMutation({
    mutationFn: async (id: string) => {
      await gqlClient.request(`mutation DeleteProxyChain($id: ID!) { deleteProxyChain(id: $id) }`, { id })
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_PROXY_CHAINS }),
  })

  const groupAddChains = useMutation({
    mutationFn: async (vars: { id: string; chainIDs: string[] }) => {
      await gqlClient.request(`mutation GroupAddChains($id: ID!, $chainIDs: [ID!]!) { groupAddChains(id: $id, chainIDs: $chainIDs) }`, vars)
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QUERY_KEY_GROUP }),
  })

  const saveGroupFailover = useMutation({
    mutationFn: async (input: Record<string, unknown>) => {
      const res = await gqlClient.request<{ saveGroupFailover: GroupFailoverStatus }>(`
        mutation SaveGroupFailover($input: SaveGroupFailoverInput!) {
          saveGroupFailover(input: $input) { groupId groupName enabled revision }
        }
      `, { input })
      return res.saveGroupFailover
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: QK_GROUP_FAILOVER })
      qc.invalidateQueries({ queryKey: QUERY_KEY_GROUP })
    },
  })

  const probeGroupFailover = useMutation({
    mutationFn: async (groupId: string) => {
      const res = await gqlClient.request<{ probeGroupFailover: GroupFailoverStatus }>(`
        mutation ProbeGroupFailover($groupId: ID!) {
          probeGroupFailover(groupId: $groupId) {
            groupId generation
            tcp { activeMemberKey consecFailures noHealthyBackup lastSwitchReason }
            udp { activeMemberKey consecFailures noHealthyBackup lastSwitchReason }
            members { memberKey tcpAlive udpAlive tcpLatMs udpLatMs lastError }
          }
        }
      `, { groupId })
      return res.probeGroupFailover
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: QK_GROUP_FAILOVER }),
  })

  const testNodePath = useMutation({
    mutationFn: async (input: { targetKind: string; targetId: string; probeType: string; destination?: string }) => {
      const res = await gqlClient.request<{ testNodePath: NodePathProbeResult }>(`
        mutation TestNodePath($input: TestNodePathInput!) {
          testNodePath(input: $input) {
            targetKind targetId targetName probeType destination protocol parseable ok latencyMs stage message testedAt
            hopResults { targetName protocol parseable ok latencyMs stage message }
          }
        }
      `, { input })
      return res.testNodePath
    },
  })

  const traceActiveRoute = useMutation({
    mutationFn: async (input: { network: string; domain?: string; dstIp?: string; dstPort: number; srcIp?: string; srcPort?: number; pname?: string }) => {
      const res = await gqlClient.request<{ traceActiveRoute: TraceRouteResult }>(`
        query TraceActiveRoute($input: TraceRouteInput!) {
          traceActiveRoute(input: $input) {
            available unavailableReason generation activeDigest decision outbound missingInputs
            rules { ruleId expression result missingInputs }
          }
        }
      `, { input })
      return res.traceActiveRoute
    },
  })

  return {
    setRoutingEditMode,
    savePolicyDraft,
    previewPolicy,
    applyPolicyRevision,
    rollbackPolicy,
    setConfigAutoApply,
    saveRuleList,
    deleteRuleList,
    refreshRuleList,
    setDnsListBindings,
    updateSubscriptionEnhanced,
    refreshSubscriptionEnhanced,
    saveProxyChain,
    deleteProxyChain,
    groupAddChains,
    saveGroupFailover,
    probeGroupFailover,
    testNodePath,
    traceActiveRoute,
  }
}
