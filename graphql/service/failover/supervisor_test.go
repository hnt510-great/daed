package failover

import (
	"sync"
	"testing"

	"github.com/daeuniverse/dae-wing/db"
)

func TestFailoverIndependentProtocolsAndStaleRejection(t *testing.T) {
	ordered := []string{"node:1", "node:2", "node:3"}
	keyToIdx := map[string]int{"node:1": 0, "node:2": 1, "node:3": 2}

	tcp := ProtoRuntimeState{Protocol: "tcp", ActiveMemberKey: "node:1", ActiveIndex: 0}
	udp := ProtoRuntimeState{Protocol: "udp", ActiveMemberKey: "node:1", ActiveIndex: 0}

	// 1. Primary loses TCP only for 3 consecutive probes -> TCP switches to node:2, UDP stays on node:1!
	for i := 0; i < 3; i++ {
		res := map[string]MemberHealth{
			"node:1": {MemberKey: "node:1", TCPAlive: false, UDPAlive: true},
			"node:2": {MemberKey: "node:2", TCPAlive: true, UDPAlive: true},
			"node:3": {MemberKey: "node:3", TCPAlive: true, UDPAlive: true},
		}
		kT, idxT, chT, _ := evalProtocolTransition(&tcp, ordered, keyToIdx, res, true, 3, 2, true)
		_, _, chU, _ := evalProtocolTransition(&udp, ordered, keyToIdx, res, false, 3, 2, true)
		if chU {
			t.Fatalf("UDP should not switch when UDP is healthy")
		}
		if i < 2 && chT {
			t.Fatalf("TCP should not switch before threshold 3 (iter %d)", i)
		}
		if i == 2 {
			if !chT || kT != "node:2" || idxT != 1 {
				t.Fatalf("TCP should switch to node:2 on 3rd failure, got ch=%v k=%s idx=%d", chT, kT, idxT)
			}
			tcp.ActiveMemberKey = kT
			tcp.ActiveIndex = idxT
			tcp.ConsecFailures = 0
		}
	}

	// 2. Primary recovers for 2 consecutive probes with autoFailback=false -> stays on node:2
	for i := 0; i < 2; i++ {
		res := map[string]MemberHealth{
			"node:1": {MemberKey: "node:1", TCPAlive: true, UDPAlive: true},
			"node:2": {MemberKey: "node:2", TCPAlive: true, UDPAlive: true},
		}
		_, _, chT, _ := evalProtocolTransition(&tcp, ordered, keyToIdx, res, true, 3, 2, false)
		if chT {
			t.Fatalf("should not failback when autoFailback=false")
		}
	}

	// 3. With autoFailback=true, 2 consecutive successes return to primary node:1
	tcp.PrimaryConsecOK = 0
	for i := 0; i < 2; i++ {
		res := map[string]MemberHealth{
			"node:1": {MemberKey: "node:1", TCPAlive: true, UDPAlive: true},
			"node:2": {MemberKey: "node:2", TCPAlive: true, UDPAlive: true},
		}
		kT, idxT, chT, _ := evalProtocolTransition(&tcp, ordered, keyToIdx, res, true, 3, 2, true)
		if i == 1 {
			if !chT || kT != "node:1" || idxT != 0 {
				t.Fatalf("expected failback to node:1, got ch=%v k=%s", chT, kT)
			}
			tcp.ActiveMemberKey = kT
			tcp.ActiveIndex = idxT
		}
	}

	// 4. All candidates fail -> keep last selection and mark NoHealthyBackup
	for i := 0; i < 3; i++ {
		res := map[string]MemberHealth{
			"node:1": {MemberKey: "node:1", TCPAlive: false},
			"node:2": {MemberKey: "node:2", TCPAlive: false},
			"node:3": {MemberKey: "node:3", TCPAlive: false},
		}
		_, _, chT, _ := evalProtocolTransition(&tcp, ordered, keyToIdx, res, true, 3, 2, true)
		if chT {
			t.Fatalf("should not switch when all candidates failed")
		}
	}
	if !tcp.NoHealthyBackup {
		t.Fatalf("expected NoHealthyBackup=true when all candidates failed")
	}

	// 5. Concurrent stale generation/revision rejection (-race test)
	sup := &Supervisor{
		gen: 5,
		runtimes: map[uint]*groupRuntime{
			1: {
				groupID:      1,
				groupName:    "proxy",
				generation:   5,
				revision:     2,
				orderedKeys:  ordered,
				keyToIndex:   keyToIdx,
				memberHealth: map[string]*MemberHealth{},
				tcp:          ProtoRuntimeState{Protocol: "tcp", ActiveMemberKey: "node:1", ActiveIndex: 0},
				udp:          ProtoRuntimeState{Protocol: "udp", ActiveMemberKey: "node:1", ActiveIndex: 0},
			},
		},
	}
	fc := &db.GroupFailover{GroupID: 1, Enabled: true, FailThreshold: 1, RecoverThreshold: 1, AutoFailback: true, Revision: 2}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(staleGen uint64) {
			defer wg.Done()
			_ = sup.ApplyProbeResults(1, staleGen, 2, fc, nil, map[string]MemberHealth{
				"node:1": {MemberKey: "node:1", TCPAlive: false, UDPAlive: false},
				"node:2": {MemberKey: "node:2", TCPAlive: true, UDPAlive: true},
			})
		}(4) // stale gen 4 != current gen 5
	}
	wg.Wait()
	if sup.runtimes[1].tcp.ConsecFailures != 0 {
		t.Fatalf("stale generation probe must not mutate runtime state")
	}
}
