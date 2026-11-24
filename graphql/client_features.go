/*
 * SPDX-License-Identifier: AGPL-3.0-only
 * Copyright (c) 2026, daeuniverse Organization
 */

package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/daeuniverse/dae-wing/common"
	"github.com/daeuniverse/dae-wing/db"
	"github.com/daeuniverse/dae-wing/graphql/service/chain"
	"github.com/daeuniverse/dae-wing/graphql/service/config"
	"github.com/daeuniverse/dae-wing/graphql/service/diagnostics"
	"github.com/daeuniverse/dae-wing/graphql/service/dns"
	"github.com/daeuniverse/dae-wing/graphql/service/failover"
	"github.com/daeuniverse/dae-wing/graphql/service/routing"
	"github.com/daeuniverse/dae-wing/graphql/service/rulelist"
	"github.com/daeuniverse/dae-wing/graphql/service/subscription"
	"github.com/graph-gophers/graphql-go"
)

func ClientFeaturesSchema() (string, error) {
	return `
type LanDeviceHint {
	hostname: String!
	ip: String!
	mac: String!
	interfaceName: String!
	source: String!
	online: Boolean!
}

type DiagnosticItem {
	scope: String!
	field: String!
	level: String!
	message: String!
}

type RuleListRef {
	objectType: String!
	objectId: ID!
	objectName: String!
}

type RuleList {
	id: ID!
	name: String!
	kind: String!
	targetGroup: String!
	sourceType: String!
	url: String!
	downloadMode: String!
	downloadGroupName: String!
	userAgent: String!
	cronExp: String!
	cronEnable: Boolean!
	rawText: String!
	lastDigest: String!
	lastEntryCount: Int!
	lastAddedCount: Int!
	lastRemovedCount: Int!
	lastStatus: String!
	lastError: String!
	lastUpdatedAt: Time
	revision: Int!
	version: Int!
	references: [RuleListRef!]!
}

input SaveRuleListInput {
	id: ID
	name: String!
	kind: String!
	targetGroup: String
	sourceType: String!
	url: String
	downloadMode: String
	downloadGroupName: String
	userAgent: String
	cronExp: String
	cronEnable: Boolean!
	rawText: String
	expectedRevision: Int
}

type SaveRuleListResult {
	ruleList: RuleList!
	diagnostics: [DiagnosticItem!]!
}

type RefreshRuleListResult {
	ruleList: RuleList!
	changed: Boolean!
	addedCount: Int!
	removedCount: Int!
	diagnostics: [DiagnosticItem!]!
}

type PolicyDraftState {
	routingId: ID!
	name: String!
	editMode: String!
	draftRevision: Int!
	policyJson: String!
	generatedRouting: String!
}

type PolicyDraftResult {
	state: PolicyDraftState!
	diagnostics: [DiagnosticItem!]!
}

type CapacityEstimate {
	estimatedMatchSets: Int!
	maxMatchSets: Int!
	ruleCount: Int!
	exceeded: Boolean!
}

type KeyValueItem {
	key: String!
	value: String!
}

type PolicyPreview {
	digest: String!
	draftRevision: Int!
	hasErrors: Boolean!
	hasEffectiveChanges: Boolean!
	reloadWarning: String!
	expandedRules: [String!]!
	redactedDiff: String!
	dependencies: [KeyValueItem!]!
	diagnostics: [DiagnosticItem!]!
	capacity: CapacityEstimate!
}

type PolicyApplyResult {
	success: Boolean!
	running: Boolean!
	rolledBack: Boolean!
	snapshotId: ID
	digest: String!
	generation: Int!
	status: String!
	message: String!
	diagnostics: [DiagnosticItem!]!
}

type ConfigSnapshot {
	id: ID!
	createdAt: Time!
	digest: String!
	summary: String!
	active: Boolean!
}

type DnsListBinding {
	listId: ID!
	upstream: String!
	qtype: String!
}

input DnsListBindingInput {
	listId: ID!
	upstream: String!
	qtype: String
}

type SubscriptionEnhanced {
	id: ID!
	format: String!
	userAgent: String!
	downloadMode: String!
	downloadGroupName: String!
	includeKeywords: String!
	excludeKeywords: String!
	filterPriority: String!
	trafficUpload: String!
	trafficDownload: String!
	trafficTotal: String!
	expireAt: Time
	lastDiffSummary: String!
	lastError: String!
	revision: Int!
}

input SubscriptionEnhancedInput {
	format: String
	userAgent: String
	downloadMode: String
	downloadGroupName: String
	includeKeywords: String
	excludeKeywords: String
	filterPriority: String
	cronExp: String
	cronEnable: Boolean
	expectedRevision: Int
}

type NodeParseError {
	index: Int!
	name: String!
	protocol: String!
	param: String!
	reason: String!
}

type RefreshSubscriptionResult {
	enhanced: SubscriptionEnhanced!
	changed: Boolean!
	addedCount: Int!
	updatedCount: Int!
	removedCount: Int!
	rebindCount: Int!
	filteredCount: Int!
	errors: [NodeParseError!]!
}

type ProxyChain {
	id: ID!
	name: String!
	tag: String
	memberNodeIds: [ID!]!
	memberNames: [String!]!
	parseable: Boolean!
	parseError: String!
	needsRebind: Boolean!
	rebindReason: String!
	version: Int!
}

input SaveProxyChainInput {
	id: ID
	name: String!
	tag: String
	memberNodeIds: [ID!]!
}

type FailoverProtoState {
	protocol: String!
	activeMemberKey: String!
	activeIndex: Int!
	consecFailures: Int!
	primaryConsecOK: Int!
	noHealthyBackup: Boolean!
	lastSwitchReason: String!
	lastSwitchAt: Time
}

type FailoverMemberHealth {
	memberKey: String!
	index: Int!
	tcpAlive: Boolean!
	udpAlive: Boolean!
	tcpLatMs: Int!
	udpLatMs: Int!
	lastError: String!
}

type GroupFailoverStatus {
	groupId: ID!
	groupName: String!
	enabled: Boolean!
	primaryMemberKey: String!
	backupMemberKeys: [String!]!
	probeIntervalSec: Int!
	probeTimeoutSec: Int!
	failThreshold: Int!
	recoverThreshold: Int!
	autoFailback: Boolean!
	probeUrl: String!
	probeDns: String!
	revision: Int!
	generation: Int!
	tcp: FailoverProtoState!
	udp: FailoverProtoState!
	members: [FailoverMemberHealth!]!
	lastProbeAt: Time
}

input SaveGroupFailoverInput {
	groupId: ID!
	enabled: Boolean!
	primaryMemberKey: String!
	backupMemberKeys: [String!]!
	probeIntervalSec: Int
	probeTimeoutSec: Int
	failThreshold: Int
	recoverThreshold: Int
	autoFailback: Boolean
	probeUrl: String
	probeDns: String
	expectedRevision: Int
}

input TestNodePathInput {
	targetKind: String!
	targetId: ID!
	probeType: String!
	destination: String
}

type NodePathProbeResult {
	targetKind: String!
	targetId: ID!
	targetName: String!
	probeType: String!
	destination: String!
	protocol: String!
	parseable: Boolean!
	ok: Boolean!
	latencyMs: Int!
	stage: String!
	message: String!
	testedAt: Time!
	hopResults: [NodePathProbeResult!]!
}

input TraceRouteInput {
	network: String!
	domain: String
	dstIp: String
	dstPort: Int!
	srcIp: String
	srcPort: Int
	pname: String
}

type TraceRuleStep {
	ruleId: String!
	expression: String!
	result: String!
	missingInputs: [String!]!
}

type TraceRouteResult {
	available: Boolean!
	unavailableReason: String!
	generation: Int!
	activeDigest: String!
	decision: String!
	outbound: String!
	missingInputs: [String!]!
	rules: [TraceRuleStep!]!
}

type SystemApplyState {
	running: Boolean!
	generation: Int!
	activeDigest: String!
	activeSnapshotId: ID
	lastApplyStatus: String!
	lastApplyError: String!
	autoApplyEnabled: Boolean!
}
`, nil
}

// --- Helper converters ---

func decodeOptionalID(id *graphql.ID) (*uint, error) {
	if id == nil || *id == "" {
		return nil, nil
	}
	v, err := common.DecodeCursor(*id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func decodeUintOrCursor(id graphql.ID) (uint, error) {
	if n, err := strconv.ParseUint(string(id), 10, 64); err == nil {
		return uint(n), nil
	}
	return common.DecodeCursor(id)
}

// --- Resolver types ---

type lanDeviceHintResolver struct{ m routing.LANDeviceHint }

func (r *lanDeviceHintResolver) Hostname() string      { return r.m.Hostname }
func (r *lanDeviceHintResolver) Ip() string            { return r.m.IP }
func (r *lanDeviceHintResolver) Mac() string           { return r.m.MAC }
func (r *lanDeviceHintResolver) InterfaceName() string { return r.m.Interface }
func (r *lanDeviceHintResolver) Source() string        { return r.m.Source }
func (r *lanDeviceHintResolver) Online() bool          { return r.m.Online }

type diagnosticItemResolver struct{ ScopeStr, FieldStr, LevelStr, MsgStr string }

func (r *diagnosticItemResolver) Scope() string   { return r.ScopeStr }
func (r *diagnosticItemResolver) Field() string   { return r.FieldStr }
func (r *diagnosticItemResolver) Level() string   { return r.LevelStr }
func (r *diagnosticItemResolver) Message() string { return r.MsgStr }

type ruleListRefResolver struct{ m rulelist.ReferenceInfo }

func (r *ruleListRefResolver) ObjectType() string { return r.m.ObjectType }
func (r *ruleListRefResolver) ObjectId() graphql.ID {
	return common.EncodeCursor(r.m.ObjectID)
}
func (r *ruleListRefResolver) ObjectName() string { return r.m.ObjectName }

type ruleListResolver struct{ m db.RuleList }

func (r *ruleListResolver) Id() graphql.ID          { return common.EncodeCursor(r.m.ID) }
func (r *ruleListResolver) Name() string            { return r.m.Name }
func (r *ruleListResolver) Kind() string            { return r.m.Kind }
func (r *ruleListResolver) TargetGroup() string     { return r.m.TargetGroup }
func (r *ruleListResolver) SourceType() string      { return r.m.SourceType }
func (r *ruleListResolver) Url() string             { return r.m.URL }
func (r *ruleListResolver) DownloadMode() string    { return r.m.DownloadMode }
func (r *ruleListResolver) DownloadGroupName() string { return r.m.DownloadGroupName }
func (r *ruleListResolver) UserAgent() string       { return r.m.UserAgent }
func (r *ruleListResolver) CronExp() string         { return r.m.CronExp }
func (r *ruleListResolver) CronEnable() bool        { return r.m.CronEnable }
func (r *ruleListResolver) RawText() string         { return r.m.RawText }
func (r *ruleListResolver) LastDigest() string      { return r.m.LastDigest }
func (r *ruleListResolver) LastEntryCount() int32   { return int32(r.m.LastEntryCount) }
func (r *ruleListResolver) LastAddedCount() int32   { return int32(r.m.LastAddedCount) }
func (r *ruleListResolver) LastRemovedCount() int32 { return int32(r.m.LastRemovedCount) }
func (r *ruleListResolver) LastStatus() string      { return r.m.LastStatus }
func (r *ruleListResolver) LastError() string       { return r.m.LastError }
func (r *ruleListResolver) LastUpdatedAt() *graphql.Time {
	if r.m.LastUpdatedAt == nil {
		return nil
	}
	t := graphql.Time{Time: *r.m.LastUpdatedAt}
	return &t
}
func (r *ruleListResolver) Revision() int32 { return int32(r.m.Revision) }
func (r *ruleListResolver) Version() int32  { return int32(r.m.Version) }
func (r *ruleListResolver) References(ctx context.Context) ([]*ruleListRefResolver, error) {
	refs, err := rulelist.FindReferences(db.DB(ctx), r.m.ID)
	if err != nil {
		return nil, err
	}
	out := make([]*ruleListRefResolver, 0, len(refs))
	for _, rf := range refs {
		out = append(out, &ruleListRefResolver{m: rf})
	}
	return out, nil
}

type saveRuleListResultResolver struct {
	rl    *db.RuleList
	diags []rulelist.ParseDiagnostic
}

func (r *saveRuleListResultResolver) RuleList() *ruleListResolver {
	return &ruleListResolver{m: *r.rl}
}
func (r *saveRuleListResultResolver) Diagnostics() []*diagnosticItemResolver {
	out := make([]*diagnosticItemResolver, 0, len(r.diags))
	for _, d := range r.diags {
		out = append(out, &diagnosticItemResolver{ScopeStr: "rulelist", FieldStr: fmt.Sprintf("line:%d", d.Line), LevelStr: d.Level, MsgStr: d.Message})
	}
	return out
}

type refreshRuleListResultResolver struct{ res *rulelist.RefreshResult }

func (r *refreshRuleListResultResolver) RuleList() *ruleListResolver {
	return &ruleListResolver{m: *r.res.RuleList}
}
func (r *refreshRuleListResultResolver) Changed() bool       { return r.res.Changed }
func (r *refreshRuleListResultResolver) AddedCount() int32   { return int32(r.res.AddedCount) }
func (r *refreshRuleListResultResolver) RemovedCount() int32 { return int32(r.res.RemovedCount) }
func (r *refreshRuleListResultResolver) Diagnostics() []*diagnosticItemResolver {
	out := make([]*diagnosticItemResolver, 0, len(r.res.Diagnostics))
	for _, d := range r.res.Diagnostics {
		out = append(out, &diagnosticItemResolver{ScopeStr: "rulelist", FieldStr: fmt.Sprintf("line:%d", d.Line), LevelStr: d.Level, MsgStr: d.Message})
	}
	return out
}

type policyDraftStateResolver struct{ m db.Routing }

func (r *policyDraftStateResolver) RoutingId() graphql.ID    { return common.EncodeCursor(r.m.ID) }
func (r *policyDraftStateResolver) Name() string             { return r.m.Name }
func (r *policyDraftStateResolver) EditMode() string         { return r.m.EditMode }
func (r *policyDraftStateResolver) DraftRevision() int32     { return int32(r.m.DraftRevision) }
func (r *policyDraftStateResolver) PolicyJson() string       { return r.m.VisualPolicyJSON }
func (r *policyDraftStateResolver) GeneratedRouting() string { return r.m.GeneratedRouting }

type policyDraftResultResolver struct {
	m     db.Routing
	diags []routing.PolicyDiagnostic
}

func (r *policyDraftResultResolver) State() *policyDraftStateResolver {
	return &policyDraftStateResolver{m: r.m}
}
func (r *policyDraftResultResolver) Diagnostics() []*diagnosticItemResolver {
	out := make([]*diagnosticItemResolver, 0, len(r.diags))
	for _, d := range r.diags {
		out = append(out, &diagnosticItemResolver{ScopeStr: "routing", FieldStr: d.Field, LevelStr: d.Level, MsgStr: d.Message})
	}
	return out
}

type capacityEstimateResolver struct{ c config.CapacityEstimate }

func (r *capacityEstimateResolver) EstimatedMatchSets() int32 { return int32(r.c.EstimatedMatchSets) }
func (r *capacityEstimateResolver) MaxMatchSets() int32       { return int32(r.c.MaxMatchSets) }
func (r *capacityEstimateResolver) RuleCount() int32          { return int32(r.c.RuleCount) }
func (r *capacityEstimateResolver) Exceeded() bool            { return r.c.Exceeded }

type keyValueResolver struct{ K, V string }

func (r *keyValueResolver) Key() string   { return r.K }
func (r *keyValueResolver) Value() string { return r.V }

type policyPreviewResolver struct{ c *config.Candidate }

func (r *policyPreviewResolver) Digest() string          { return r.c.Digest }
func (r *policyPreviewResolver) DraftRevision() int32    { return int32(r.c.RoutingModel.DraftRevision) }
func (r *policyPreviewResolver) HasErrors() bool         { return r.c.HasErrors() }
func (r *policyPreviewResolver) HasEffectiveChanges() bool { return r.c.HasEffectiveChanges }
func (r *policyPreviewResolver) ReloadWarning() string {
	if !r.c.HasEffectiveChanges {
		return ""
	}
	return "Applying performs a full control plane reload (~1s interruption for existing proxied connections)."
}
func (r *policyPreviewResolver) ExpandedRules() []string { return r.c.ExpandedRules }
func (r *policyPreviewResolver) RedactedDiff() string    { return r.c.RedactedDiff }
func (r *policyPreviewResolver) Dependencies() []*keyValueResolver {
	out := make([]*keyValueResolver, 0, len(r.c.DependencyVersions))
	for k, v := range r.c.DependencyVersions {
		out = append(out, &keyValueResolver{K: k, V: v})
	}
	return out
}
func (r *policyPreviewResolver) Diagnostics() []*diagnosticItemResolver {
	out := make([]*diagnosticItemResolver, 0, len(r.c.Diagnostics))
	for _, d := range r.c.Diagnostics {
		out = append(out, &diagnosticItemResolver{ScopeStr: d.Scope, FieldStr: d.Field, LevelStr: d.Level, MsgStr: d.Message})
	}
	return out
}
func (r *policyPreviewResolver) Capacity() *capacityEstimateResolver {
	return &capacityEstimateResolver{c: r.c.Capacity}
}

type policyApplyResultResolver struct{ res *config.ApplyResult }

func (r *policyApplyResultResolver) Success() bool    { return r.res.Success }
func (r *policyApplyResultResolver) Running() bool    { return r.res.Running }
func (r *policyApplyResultResolver) RolledBack() bool { return r.res.RolledBack }
func (r *policyApplyResultResolver) SnapshotId() *graphql.ID {
	if r.res.SnapshotID == 0 {
		return nil
	}
	id := common.EncodeCursor(r.res.SnapshotID)
	return &id
}
func (r *policyApplyResultResolver) Digest() string    { return r.res.Digest }
func (r *policyApplyResultResolver) Generation() int32 { return int32(r.res.Generation) }
func (r *policyApplyResultResolver) Status() string    { return r.res.Status }
func (r *policyApplyResultResolver) Message() string   { return r.res.Message }
func (r *policyApplyResultResolver) Diagnostics() []*diagnosticItemResolver {
	out := make([]*diagnosticItemResolver, 0, len(r.res.Diagnostics))
	for _, d := range r.res.Diagnostics {
		out = append(out, &diagnosticItemResolver{ScopeStr: d.Scope, FieldStr: d.Field, LevelStr: d.Level, MsgStr: d.Message})
	}
	return out
}

type configSnapshotResolver struct{ m db.ConfigSnapshot }

func (r *configSnapshotResolver) Id() graphql.ID       { return common.EncodeCursor(r.m.ID) }
func (r *configSnapshotResolver) CreatedAt() graphql.Time { return graphql.Time{Time: r.m.CreatedAt} }
func (r *configSnapshotResolver) Digest() string       { return r.m.Digest }
func (r *configSnapshotResolver) Summary() string      { return r.m.Summary }
func (r *configSnapshotResolver) Active() bool         { return r.m.Active }

type dnsListBindingResolver struct{ b dns.DomainListBinding }

func (r *dnsListBindingResolver) ListId() graphql.ID { return common.EncodeCursor(r.b.ListID) }
func (r *dnsListBindingResolver) Upstream() string   { return r.b.Upstream }
func (r *dnsListBindingResolver) Qtype() string      { return r.b.QType }

type subscriptionEnhancedResolver struct{ m db.Subscription }

func (r *subscriptionEnhancedResolver) Id() graphql.ID          { return common.EncodeCursor(r.m.ID) }
func (r *subscriptionEnhancedResolver) Format() string          { return r.m.Format }
func (r *subscriptionEnhancedResolver) UserAgent() string       { return r.m.UserAgent }
func (r *subscriptionEnhancedResolver) DownloadMode() string    { return r.m.DownloadMode }
func (r *subscriptionEnhancedResolver) DownloadGroupName() string { return r.m.DownloadGroupName }
func (r *subscriptionEnhancedResolver) IncludeKeywords() string { return r.m.IncludeKeywords }
func (r *subscriptionEnhancedResolver) ExcludeKeywords() string { return r.m.ExcludeKeywords }
func (r *subscriptionEnhancedResolver) FilterPriority() string  { return r.m.FilterPriority }
func (r *subscriptionEnhancedResolver) TrafficUpload() string   { return strconv.FormatInt(r.m.TrafficUpload, 10) }
func (r *subscriptionEnhancedResolver) TrafficDownload() string { return strconv.FormatInt(r.m.TrafficDownload, 10) }
func (r *subscriptionEnhancedResolver) TrafficTotal() string    { return strconv.FormatInt(r.m.TrafficTotal, 10) }
func (r *subscriptionEnhancedResolver) ExpireAt() *graphql.Time {
	if r.m.ExpireAt == nil {
		return nil
	}
	t := graphql.Time{Time: *r.m.ExpireAt}
	return &t
}
func (r *subscriptionEnhancedResolver) LastDiffSummary() string { return r.m.LastDiffSummary }
func (r *subscriptionEnhancedResolver) LastError() string       { return r.m.LastError }
func (r *subscriptionEnhancedResolver) Revision() int32         { return int32(r.m.Revision) }

type nodeParseErrorResolver struct{ e subscription.NodeParseError }

func (r *nodeParseErrorResolver) Index() int32     { return int32(r.e.Index) }
func (r *nodeParseErrorResolver) Name() string     { return r.e.Name }
func (r *nodeParseErrorResolver) Protocol() string { return r.e.Protocol }
func (r *nodeParseErrorResolver) Param() string    { return r.e.Param }
func (r *nodeParseErrorResolver) Reason() string   { return r.e.Reason }

type refreshSubscriptionResultResolver struct{ s *subscription.RefreshSummary }

func (r *refreshSubscriptionResultResolver) Enhanced() *subscriptionEnhancedResolver {
	return &subscriptionEnhancedResolver{m: *r.s.Subscription}
}
func (r *refreshSubscriptionResultResolver) Changed() bool       { return r.s.Changed }
func (r *refreshSubscriptionResultResolver) AddedCount() int32   { return int32(r.s.AddedCount) }
func (r *refreshSubscriptionResultResolver) UpdatedCount() int32 { return int32(r.s.UpdatedCount) }
func (r *refreshSubscriptionResultResolver) RemovedCount() int32 { return int32(r.s.RemovedCount) }
func (r *refreshSubscriptionResultResolver) RebindCount() int32  { return int32(r.s.RebindCount) }
func (r *refreshSubscriptionResultResolver) FilteredCount() int32 { return int32(r.s.FilteredCount) }
func (r *refreshSubscriptionResultResolver) Errors() []*nodeParseErrorResolver {
	out := make([]*nodeParseErrorResolver, 0, len(r.s.Errors))
	for _, e := range r.s.Errors {
		out = append(out, &nodeParseErrorResolver{e: e})
	}
	return out
}

type proxyChainResolver struct{ info chain.ChainInfo }

func (r *proxyChainResolver) Id() graphql.ID { return common.EncodeCursor(r.info.Chain.ID) }
func (r *proxyChainResolver) Name() string   { return r.info.Chain.Name }
func (r *proxyChainResolver) Tag() *string   { return r.info.Chain.Tag }
func (r *proxyChainResolver) MemberNodeIds() []graphql.ID {
	out := make([]graphql.ID, 0, len(r.info.MemberIDs))
	for _, id := range r.info.MemberIDs {
		out = append(out, common.EncodeCursor(id))
	}
	return out
}
func (r *proxyChainResolver) MemberNames() []string { return r.info.MemberNames }
func (r *proxyChainResolver) Parseable() bool       { return r.info.Parseable }
func (r *proxyChainResolver) ParseError() string    { return r.info.ParseError }
func (r *proxyChainResolver) NeedsRebind() bool     { return r.info.Chain.NeedsRebind }
func (r *proxyChainResolver) RebindReason() string  { return r.info.Chain.RebindReason }
func (r *proxyChainResolver) Version() int32        { return int32(r.info.Chain.Version) }

type failoverProtoStateResolver struct{ s failover.ProtoRuntimeState }

func (r *failoverProtoStateResolver) Protocol() string        { return r.s.Protocol }
func (r *failoverProtoStateResolver) ActiveMemberKey() string { return r.s.ActiveMemberKey }
func (r *failoverProtoStateResolver) ActiveIndex() int32      { return int32(r.s.ActiveIndex) }
func (r *failoverProtoStateResolver) ConsecFailures() int32   { return int32(r.s.ConsecFailures) }
func (r *failoverProtoStateResolver) PrimaryConsecOK() int32  { return int32(r.s.PrimaryConsecOK) }
func (r *failoverProtoStateResolver) NoHealthyBackup() bool   { return r.s.NoHealthyBackup }
func (r *failoverProtoStateResolver) LastSwitchReason() string { return r.s.LastSwitchReason }
func (r *failoverProtoStateResolver) LastSwitchAt() *graphql.Time {
	if r.s.LastSwitchAt == nil {
		return nil
	}
	t := graphql.Time{Time: *r.s.LastSwitchAt}
	return &t
}

type failoverMemberHealthResolver struct{ m failover.MemberHealth }

func (r *failoverMemberHealthResolver) MemberKey() string { return r.m.MemberKey }
func (r *failoverMemberHealthResolver) Index() int32      { return int32(r.m.Index) }
func (r *failoverMemberHealthResolver) TcpAlive() bool    { return r.m.TCPAlive }
func (r *failoverMemberHealthResolver) UdpAlive() bool    { return r.m.UDPAlive }
func (r *failoverMemberHealthResolver) TcpLatMs() int32   { return int32(r.m.TCPLatMs) }
func (r *failoverMemberHealthResolver) UdpLatMs() int32   { return int32(r.m.UDPLatMs) }
func (r *failoverMemberHealthResolver) LastError() string { return r.m.LastError }

type groupFailoverStatusResolver struct{ st *failover.GroupFailoverStatus }

func (r *groupFailoverStatusResolver) GroupId() graphql.ID     { return common.EncodeCursor(r.st.Config.GroupID) }
func (r *groupFailoverStatusResolver) GroupName() string       { return r.st.GroupName }
func (r *groupFailoverStatusResolver) Enabled() bool           { return r.st.Config.Enabled }
func (r *groupFailoverStatusResolver) PrimaryMemberKey() string { return r.st.Config.PrimaryMemberKey }
func (r *groupFailoverStatusResolver) BackupMemberKeys() []string {
	var keys []string
	_ = json.Unmarshal([]byte(r.st.Config.BackupMemberKeysJSON), &keys)
	if keys == nil {
		return []string{}
	}
	return keys
}
func (r *groupFailoverStatusResolver) ProbeIntervalSec() int32 { return int32(r.st.Config.ProbeIntervalSec) }
func (r *groupFailoverStatusResolver) ProbeTimeoutSec() int32  { return int32(r.st.Config.ProbeTimeoutSec) }
func (r *groupFailoverStatusResolver) FailThreshold() int32    { return int32(r.st.Config.FailThreshold) }
func (r *groupFailoverStatusResolver) RecoverThreshold() int32 { return int32(r.st.Config.RecoverThreshold) }
func (r *groupFailoverStatusResolver) AutoFailback() bool      { return r.st.Config.AutoFailback }
func (r *groupFailoverStatusResolver) ProbeUrl() string        { return r.st.Config.ProbeURL }
func (r *groupFailoverStatusResolver) ProbeDns() string        { return r.st.Config.ProbeDNS }
func (r *groupFailoverStatusResolver) Revision() int32         { return int32(r.st.Config.Revision) }
func (r *groupFailoverStatusResolver) Generation() int32       { return int32(r.st.Generation) }
func (r *groupFailoverStatusResolver) Tcp() *failoverProtoStateResolver {
	return &failoverProtoStateResolver{s: r.st.TCP}
}
func (r *groupFailoverStatusResolver) Udp() *failoverProtoStateResolver {
	return &failoverProtoStateResolver{s: r.st.UDP}
}
func (r *groupFailoverStatusResolver) Members() []*failoverMemberHealthResolver {
	out := make([]*failoverMemberHealthResolver, 0, len(r.st.Members))
	for _, m := range r.st.Members {
		out = append(out, &failoverMemberHealthResolver{m: m})
	}
	return out
}
func (r *groupFailoverStatusResolver) LastProbeAt() *graphql.Time {
	if r.st.LastProbeAt == nil {
		return nil
	}
	t := graphql.Time{Time: *r.st.LastProbeAt}
	return &t
}

type nodePathProbeResultResolver struct{ res diagnostics.NodePathProbeResult }

func (r *nodePathProbeResultResolver) TargetKind() string     { return r.res.TargetKind }
func (r *nodePathProbeResultResolver) TargetId() graphql.ID   { return common.EncodeCursor(r.res.TargetID) }
func (r *nodePathProbeResultResolver) TargetName() string     { return r.res.TargetName }
func (r *nodePathProbeResultResolver) ProbeType() string      { return r.res.ProbeType }
func (r *nodePathProbeResultResolver) Destination() string    { return r.res.Destination }
func (r *nodePathProbeResultResolver) Protocol() string       { return r.res.Protocol }
func (r *nodePathProbeResultResolver) Parseable() bool        { return r.res.Parseable }
func (r *nodePathProbeResultResolver) Ok() bool               { return r.res.OK }
func (r *nodePathProbeResultResolver) LatencyMs() int32       { return int32(r.res.LatencyMs) }
func (r *nodePathProbeResultResolver) Stage() string          { return r.res.Stage }
func (r *nodePathProbeResultResolver) Message() string        { return r.res.Message }
func (r *nodePathProbeResultResolver) TestedAt() graphql.Time  { return graphql.Time{Time: r.res.TestedAt} }
func (r *nodePathProbeResultResolver) HopResults() []*nodePathProbeResultResolver {
	out := make([]*nodePathProbeResultResolver, 0, len(r.res.HopResults))
	for _, h := range r.res.HopResults {
		out = append(out, &nodePathProbeResultResolver{res: h})
	}
	return out
}

type traceRuleStepResolver struct{ s diagnostics.TraceRuleStep }

func (r *traceRuleStepResolver) RuleId() string        { return r.s.RuleID }
func (r *traceRuleStepResolver) Expression() string    { return r.s.Expression }
func (r *traceRuleStepResolver) Result() string        { return r.s.Result }
func (r *traceRuleStepResolver) MissingInputs() []string {
	if r.s.MissingInputs == nil {
		return []string{}
	}
	return r.s.MissingInputs
}

type traceRouteResultResolver struct{ res *diagnostics.TraceRouteResult }

func (r *traceRouteResultResolver) Available() bool           { return r.res.Available }
func (r *traceRouteResultResolver) UnavailableReason() string { return r.res.UnavailableReason }
func (r *traceRouteResultResolver) Generation() int32         { return int32(r.res.Generation) }
func (r *traceRouteResultResolver) ActiveDigest() string      { return r.res.ActiveDigest }
func (r *traceRouteResultResolver) Decision() string          { return r.res.Decision }
func (r *traceRouteResultResolver) Outbound() string          { return r.res.Outbound }
func (r *traceRouteResultResolver) MissingInputs() []string {
	if r.res.MissingInputs == nil {
		return []string{}
	}
	return r.res.MissingInputs
}
func (r *traceRouteResultResolver) Rules() []*traceRuleStepResolver {
	out := make([]*traceRuleStepResolver, 0, len(r.res.Rules))
	for _, s := range r.res.Rules {
		out = append(out, &traceRuleStepResolver{s: s})
	}
	return out
}

type systemApplyStateResolver struct {
	sys       db.System
	autoApply bool
}

func (r *systemApplyStateResolver) Running() bool        { return r.sys.Running }
func (r *systemApplyStateResolver) Generation() int32    { return int32(r.sys.Generation) }
func (r *systemApplyStateResolver) ActiveDigest() string { return r.sys.ActiveDigest }
func (r *systemApplyStateResolver) ActiveSnapshotId() *graphql.ID {
	if r.sys.ActiveSnapshotID == nil || *r.sys.ActiveSnapshotID == 0 {
		return nil
	}
	id := common.EncodeCursor(*r.sys.ActiveSnapshotID)
	return &id
}
func (r *systemApplyStateResolver) LastApplyStatus() string { return r.sys.LastApplyStatus }
func (r *systemApplyStateResolver) LastApplyError() string  { return r.sys.LastApplyError }
func (r *systemApplyStateResolver) AutoApplyEnabled() bool  { return r.autoApply }

// --- Query methods on queryResolver ---

func (r *queryResolver) LanDevices() []*lanDeviceHintResolver {
	devs := routing.DiscoverLANDevices()
	out := make([]*lanDeviceHintResolver, 0, len(devs))
	for _, d := range devs {
		out = append(out, &lanDeviceHintResolver{m: d})
	}
	return out
}

func (r *queryResolver) RuleLists(ctx context.Context) ([]*ruleListResolver, error) {
	lists, err := rulelist.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*ruleListResolver, 0, len(lists))
	for _, l := range lists {
		out = append(out, &ruleListResolver{m: l})
	}
	return out, nil
}

func (r *queryResolver) PolicyDraft(ctx context.Context, args struct{ RoutingId graphql.ID }) (*policyDraftStateResolver, error) {
	id, err := decodeUintOrCursor(args.RoutingId)
	if err != nil {
		return nil, err
	}
	var m db.Routing
	if err := db.DB(ctx).Where("id = ?", id).First(&m).Error; err != nil {
		return nil, err
	}
	return &policyDraftStateResolver{m: m}, nil
}

func (r *queryResolver) PreviewPolicy(ctx context.Context, args struct {
	ConfigId  *graphql.ID
	DnsId     *graphql.ID
	RoutingId *graphql.ID
}) (*policyPreviewResolver, error) {
	cid, err := decodeOptionalID(args.ConfigId)
	if err != nil {
		return nil, err
	}
	did, err := decodeOptionalID(args.DnsId)
	if err != nil {
		return nil, err
	}
	rid, err := decodeOptionalID(args.RoutingId)
	if err != nil {
		return nil, err
	}
	cand, err := config.PreviewPolicy(ctx, config.AssembleOptions{
		ConfigID:  cid,
		DnsID:     did,
		RoutingID: rid,
	})
	if err != nil {
		return nil, err
	}
	return &policyPreviewResolver{c: cand}, nil
}

func (r *queryResolver) ConfigSnapshots(ctx context.Context) ([]*configSnapshotResolver, error) {
	snaps, err := config.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*configSnapshotResolver, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, &configSnapshotResolver{m: s})
	}
	return out, nil
}

func (r *queryResolver) ProxyChains(ctx context.Context) ([]*proxyChainResolver, error) {
	chains, err := chain.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*proxyChainResolver, 0, len(chains))
	for _, c := range chains {
		out = append(out, &proxyChainResolver{info: c})
	}
	return out, nil
}

func (r *queryResolver) GroupFailover(ctx context.Context, args struct{ GroupId graphql.ID }) (*groupFailoverStatusResolver, error) {
	gid, err := decodeUintOrCursor(args.GroupId)
	if err != nil {
		return nil, err
	}
	st, err := failover.Default().GetStatus(ctx, gid)
	if err != nil {
		return nil, err
	}
	return &groupFailoverStatusResolver{st: st}, nil
}

func (r *queryResolver) SubscriptionEnhanced(ctx context.Context, args struct{ Id graphql.ID }) (*subscriptionEnhancedResolver, error) {
	sid, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return nil, err
	}
	var m db.Subscription
	if err := db.DB(ctx).Where("id = ?", sid).First(&m).Error; err != nil {
		return nil, err
	}
	return &subscriptionEnhancedResolver{m: m}, nil
}

func (r *queryResolver) DnsListBindings(ctx context.Context, args struct{ DnsId graphql.ID }) ([]*dnsListBindingResolver, error) {
	did, err := decodeUintOrCursor(args.DnsId)
	if err != nil {
		return nil, err
	}
	var m db.Dns
	if err := db.DB(ctx).Where("id = ?", did).First(&m).Error; err != nil {
		return nil, err
	}
	bindings, err := dns.ParseDomainListBindings(m.DomainListRefsJSON)
	if err != nil {
		return nil, err
	}
	out := make([]*dnsListBindingResolver, 0, len(bindings))
	for _, b := range bindings {
		out = append(out, &dnsListBindingResolver{b: b})
	}
	return out, nil
}

func (r *queryResolver) TraceActiveRoute(ctx context.Context, args struct {
	Input struct {
		Network string
		Domain  *string
		DstIp   *string
		DstPort int32
		SrcIp   *string
		SrcPort *int32
		Pname   *string
	}
}) (*traceRouteResultResolver, error) {
	var sp *uint16
	if args.Input.SrcPort != nil {
		v := uint16(*args.Input.SrcPort)
		sp = &v
	}
	res, err := diagnostics.TraceActiveRoute(ctx, diagnostics.TraceRouteInput{
		Network: args.Input.Network,
		Domain:  args.Input.Domain,
		DstIP:   args.Input.DstIp,
		DstPort: uint16(args.Input.DstPort),
		SrcIP:   args.Input.SrcIp,
		SrcPort: sp,
		Pname:   args.Input.Pname,
	})
	if err != nil {
		return nil, err
	}
	return &traceRouteResultResolver{res: res}, nil
}

func (r *queryResolver) SystemApplyState(ctx context.Context) (*systemApplyStateResolver, error) {
	var sys db.System
	if err := db.DB(ctx).Model(&db.System{}).FirstOrCreate(&sys).Error; err != nil {
		return nil, err
	}
	var cfg db.Config
	autoApply := false
	if err := db.DB(ctx).Where("selected = ?", true).First(&cfg).Error; err == nil {
		autoApply = cfg.AutoApply
	}
	return &systemApplyStateResolver{sys: sys, autoApply: autoApply}, nil
}

// --- Mutation methods on MutationResolver ---

func (r *MutationResolver) SavePolicyDraft(ctx context.Context, args struct {
	RoutingId        graphql.ID
	ExpectedRevision *int32
	PolicyJson       string
}) (*policyDraftResultResolver, error) {
	rid, err := decodeUintOrCursor(args.RoutingId)
	if err != nil {
		return nil, err
	}
	var expRev *uint
	if args.ExpectedRevision != nil {
		v := uint(*args.ExpectedRevision)
		expRev = &v
	}
	var vp routing.VisualPolicy
	if err := json.Unmarshal([]byte(args.PolicyJson), &vp); err != nil {
		return nil, fmt.Errorf("invalid policyJson: %w", err)
	}
	m, diags, err := routing.SavePolicyDraft(ctx, rid, expRev, vp)
	if err != nil {
		return nil, err
	}
	return &policyDraftResultResolver{m: *m, diags: diags}, nil
}

func (r *MutationResolver) SetRoutingEditMode(ctx context.Context, args struct {
	RoutingId graphql.ID
	EditMode  string
}) (*policyDraftStateResolver, error) {
	rid, err := decodeUintOrCursor(args.RoutingId)
	if err != nil {
		return nil, err
	}
	m, err := routing.SetEditMode(ctx, rid, args.EditMode)
	if err != nil {
		return nil, err
	}
	return &policyDraftStateResolver{m: *m}, nil
}

func (r *MutationResolver) CloneRoutingAsRaw(ctx context.Context, args struct {
	RoutingId graphql.ID
	NewName   *string
}) (*routing.Resolver, error) {
	rid, err := decodeUintOrCursor(args.RoutingId)
	if err != nil {
		return nil, err
	}
	name := ""
	if args.NewName != nil {
		name = *args.NewName
	}
	m, err := routing.CloneAsRawCopy(ctx, rid, name)
	if err != nil {
		return nil, err
	}
	return &routing.Resolver{Model: m}, nil
}

func (r *MutationResolver) ApplyPolicyRevision(ctx context.Context, args struct {
	ExpectedDraftRevision *int32
	ExpectedDigest        *string
	ConfigId              *graphql.ID
	DnsId                 *graphql.ID
	RoutingId             *graphql.ID
}) (*policyApplyResultResolver, error) {
	var expRev *uint
	if args.ExpectedDraftRevision != nil {
		v := uint(*args.ExpectedDraftRevision)
		expRev = &v
	}
	cid, err := decodeOptionalID(args.ConfigId)
	if err != nil {
		return nil, err
	}
	did, err := decodeOptionalID(args.DnsId)
	if err != nil {
		return nil, err
	}
	rid, err := decodeOptionalID(args.RoutingId)
	if err != nil {
		return nil, err
	}
	res, err := config.ApplyPolicyRevision(ctx, expRev, args.ExpectedDigest, config.AssembleOptions{
		ConfigID:  cid,
		DnsID:     did,
		RoutingID: rid,
	})
	if res != nil {
		return &policyApplyResultResolver{res: res}, err
	}
	return nil, err
}

func (r *MutationResolver) RollbackPolicy(ctx context.Context, args struct{ SnapshotId graphql.ID }) (*policyApplyResultResolver, error) {
	sid, err := decodeUintOrCursor(args.SnapshotId)
	if err != nil {
		return nil, err
	}
	res, err := config.RollbackPolicy(ctx, sid)
	if err != nil {
		return nil, err
	}
	return &policyApplyResultResolver{res: res}, nil
}

func (r *MutationResolver) SetConfigAutoApply(ctx context.Context, args struct {
	ConfigId  graphql.ID
	AutoApply bool
}) (bool, error) {
	cid, err := decodeUintOrCursor(args.ConfigId)
	if err != nil {
		return false, err
	}
	if _, err := config.SetAutoApply(ctx, cid, args.AutoApply); err != nil {
		return false, err
	}
	return true, nil
}

func (r *MutationResolver) SetDnsListBindings(ctx context.Context, args struct {
	DnsId    graphql.ID
	Bindings []struct {
		ListId   graphql.ID
		Upstream string
		Qtype    *string
	}
}) (bool, error) {
	did, err := decodeUintOrCursor(args.DnsId)
	if err != nil {
		return false, err
	}
	bs := make([]dns.DomainListBinding, 0, len(args.Bindings))
	for _, b := range args.Bindings {
		lid, err := decodeUintOrCursor(b.ListId)
		if err != nil {
			return false, err
		}
		qt := ""
		if b.Qtype != nil {
			qt = *b.Qtype
		}
		bs = append(bs, dns.DomainListBinding{ListID: lid, Upstream: b.Upstream, QType: qt})
	}
	if _, err := dns.SetDomainListBindings(ctx, did, bs); err != nil {
		return false, err
	}
	return true, nil
}

func (r *MutationResolver) SaveRuleList(ctx context.Context, args struct {
	Input struct {
		Id                *graphql.ID
		Name              string
		Kind              string
		TargetGroup       *string
		SourceType        string
		Url               *string
		DownloadMode      *string
		DownloadGroupName *string
		UserAgent         *string
		CronExp           *string
		CronEnable        bool
		RawText           *string
		ExpectedRevision  *int32
	}
}) (*saveRuleListResultResolver, error) {
	id, err := decodeOptionalID(args.Input.Id)
	if err != nil {
		return nil, err
	}
	var expRev *uint
	if args.Input.ExpectedRevision != nil {
		v := uint(*args.Input.ExpectedRevision)
		expRev = &v
	}
	deref := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	rl, diags, err := rulelist.Save(ctx, rulelist.SaveInput{
		ID:                id,
		Name:              args.Input.Name,
		Kind:              args.Input.Kind,
		TargetGroup:       deref(args.Input.TargetGroup),
		SourceType:        args.Input.SourceType,
		URL:               deref(args.Input.Url),
		DownloadMode:      deref(args.Input.DownloadMode),
		DownloadGroupName: deref(args.Input.DownloadGroupName),
		UserAgent:         deref(args.Input.UserAgent),
		CronExp:           deref(args.Input.CronExp),
		CronEnable:        args.Input.CronEnable,
		RawText:           deref(args.Input.RawText),
		ExpectedRevision:  expRev,
	})
	if err != nil {
		return nil, err
	}
	return &saveRuleListResultResolver{rl: rl, diags: diags}, nil
}

func (r *MutationResolver) DeleteRuleList(ctx context.Context, args struct{ Id graphql.ID }) (bool, error) {
	id, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return false, err
	}
	if err := rulelist.Delete(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

func (r *MutationResolver) RefreshRuleList(ctx context.Context, args struct{ Id graphql.ID }) (*refreshRuleListResultResolver, error) {
	id, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return nil, err
	}
	res, err := rulelist.RefreshByID(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return &refreshRuleListResultResolver{res: res}, nil
}

func (r *MutationResolver) UpdateSubscriptionEnhanced(ctx context.Context, args struct {
	Id    graphql.ID
	Input struct {
		Format            *string
		UserAgent         *string
		DownloadMode      *string
		DownloadGroupName *string
		IncludeKeywords   *string
		ExcludeKeywords   *string
		FilterPriority    *string
		CronExp           *string
		CronEnable        *bool
		ExpectedRevision  *int32
	}
}) (*subscriptionEnhancedResolver, error) {
	sid, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return nil, err
	}
	var expRev *uint
	if args.Input.ExpectedRevision != nil {
		v := uint(*args.Input.ExpectedRevision)
		expRev = &v
	}
	m, err := subscription.UpdateEnhancedSettings(ctx, sid, subscription.EnhancedSettingsInput{
		Format:            args.Input.Format,
		UserAgent:         args.Input.UserAgent,
		DownloadMode:      args.Input.DownloadMode,
		DownloadGroupName: args.Input.DownloadGroupName,
		IncludeKeywords:   args.Input.IncludeKeywords,
		ExcludeKeywords:   args.Input.ExcludeKeywords,
		FilterPriority:    args.Input.FilterPriority,
		CronExp:           args.Input.CronExp,
		CronEnable:        args.Input.CronEnable,
		ExpectedRevision:  expRev,
	})
	if err != nil {
		return nil, err
	}
	return &subscriptionEnhancedResolver{m: *m}, nil
}

func (r *MutationResolver) RefreshSubscriptionEnhanced(ctx context.Context, args struct{ Id graphql.ID }) (*refreshSubscriptionResultResolver, error) {
	sid, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return nil, err
	}
	res, err := subscription.RefreshStableByID(ctx, sid, false)
	if err != nil {
		return nil, err
	}
	return &refreshSubscriptionResultResolver{s: res}, nil
}

func (r *MutationResolver) SaveProxyChain(ctx context.Context, args struct {
	Input struct {
		Id            *graphql.ID
		Name          string
		Tag           *string
		MemberNodeIds []graphql.ID
	}
}) (*proxyChainResolver, error) {
	id, err := decodeOptionalID(args.Input.Id)
	if err != nil {
		return nil, err
	}
	memberIDs := make([]uint, 0, len(args.Input.MemberNodeIds))
	for _, mid := range args.Input.MemberNodeIds {
		v, err := decodeUintOrCursor(mid)
		if err != nil {
			return nil, err
		}
		memberIDs = append(memberIDs, v)
	}
	info, err := chain.Save(ctx, chain.SaveChainInput{
		ID:            id,
		Name:          args.Input.Name,
		Tag:           args.Input.Tag,
		MemberNodeIDs: memberIDs,
	})
	if err != nil {
		return nil, err
	}
	return &proxyChainResolver{info: *info}, nil
}

func (r *MutationResolver) DeleteProxyChain(ctx context.Context, args struct{ Id graphql.ID }) (bool, error) {
	id, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return false, err
	}
	if err := chain.Delete(ctx, id); err != nil {
		return false, err
	}
	return true, nil
}

func (r *MutationResolver) GroupAddChains(ctx context.Context, args struct {
	Id       graphql.ID
	ChainIDs []graphql.ID
}) (int32, error) {
	gid, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return 0, err
	}
	cids := make([]uint, 0, len(args.ChainIDs))
	for _, cid := range args.ChainIDs {
		v, err := decodeUintOrCursor(cid)
		if err != nil {
			return 0, err
		}
		cids = append(cids, v)
	}
	return chain.GroupAddChains(ctx, gid, cids)
}

func (r *MutationResolver) GroupDelChains(ctx context.Context, args struct {
	Id       graphql.ID
	ChainIDs []graphql.ID
}) (int32, error) {
	gid, err := decodeUintOrCursor(args.Id)
	if err != nil {
		return 0, err
	}
	cids := make([]uint, 0, len(args.ChainIDs))
	for _, cid := range args.ChainIDs {
		v, err := decodeUintOrCursor(cid)
		if err != nil {
			return 0, err
		}
		cids = append(cids, v)
	}
	return chain.GroupDelChains(ctx, gid, cids)
}

func (r *MutationResolver) SaveGroupFailover(ctx context.Context, args struct {
	Input struct {
		GroupId          graphql.ID
		Enabled          bool
		PrimaryMemberKey string
		BackupMemberKeys []string
		ProbeIntervalSec *int32
		ProbeTimeoutSec  *int32
		FailThreshold    *int32
		RecoverThreshold *int32
		AutoFailback     *bool
		ProbeUrl         *string
		ProbeDns         *string
		ExpectedRevision *int32
	}
}) (*groupFailoverStatusResolver, error) {
	gid, err := decodeUintOrCursor(args.Input.GroupId)
	if err != nil {
		return nil, err
	}
	toIntPtr := func(p *int32) *int {
		if p == nil {
			return nil
		}
		v := int(*p)
		return &v
	}
	var expRev *uint
	if args.Input.ExpectedRevision != nil {
		v := uint(*args.Input.ExpectedRevision)
		expRev = &v
	}
	if _, err := failover.SaveFailoverConfig(ctx, failover.SaveFailoverInput{
		GroupID:          gid,
		Enabled:          args.Input.Enabled,
		PrimaryMemberKey: args.Input.PrimaryMemberKey,
		BackupMemberKeys: args.Input.BackupMemberKeys,
		ProbeIntervalSec: toIntPtr(args.Input.ProbeIntervalSec),
		ProbeTimeoutSec:  toIntPtr(args.Input.ProbeTimeoutSec),
		FailThreshold:    toIntPtr(args.Input.FailThreshold),
		RecoverThreshold: toIntPtr(args.Input.RecoverThreshold),
		AutoFailback:     args.Input.AutoFailback,
		ProbeURL:         args.Input.ProbeUrl,
		ProbeDNS:         args.Input.ProbeDns,
		ExpectedRevision: expRev,
	}); err != nil {
		return nil, err
	}
	st, err := failover.Default().GetStatus(ctx, gid)
	if err != nil {
		return nil, err
	}
	return &groupFailoverStatusResolver{st: st}, nil
}

func (r *MutationResolver) ProbeGroupFailover(ctx context.Context, args struct{ GroupId graphql.ID }) (*groupFailoverStatusResolver, error) {
	gid, err := decodeUintOrCursor(args.GroupId)
	if err != nil {
		return nil, err
	}
	if err := failover.Default().ProbeGroupOnce(ctx, gid); err != nil {
		return nil, err
	}
	st, err := failover.Default().GetStatus(ctx, gid)
	if err != nil {
		return nil, err
	}
	return &groupFailoverStatusResolver{st: st}, nil
}

func (r *MutationResolver) TestNodePath(ctx context.Context, args struct {
	Input struct {
		TargetKind  string
		TargetId    graphql.ID
		ProbeType   string
		Destination *string
	}
}) (*nodePathProbeResultResolver, error) {
	tid, err := decodeUintOrCursor(args.Input.TargetId)
	if err != nil {
		return nil, err
	}
	dest := ""
	if args.Input.Destination != nil {
		dest = *args.Input.Destination
	}
	res, err := diagnostics.TestNodePath(ctx, args.Input.TargetKind, tid, args.Input.ProbeType, dest)
	if err != nil {
		return nil, err
	}
	return &nodePathProbeResultResolver{res: *res}, nil
}
