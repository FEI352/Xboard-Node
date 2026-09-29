package machine

import (
	"testing"

	"github.com/cedar2025/xboard-node/internal/controlplane"
)

func TestRemoveExitedNodeOnlyRemovesMatchingHandle(t *testing.T) {
	oldMailbox := controlplane.NewNodeMailbox()
	old := &nodeHandle{mailbox: oldMailbox}
	orchestrator := &Orchestrator{
		nodes:     map[int]*nodeHandle{6: old},
		mailboxes: map[int]*controlplane.NodeMailbox{6: oldMailbox},
		statuses:  make(map[int]chan<- controlplane.StatusChange),
	}

	orchestrator.removeExitedNode(6, old)

	if _, ok := orchestrator.nodes[6]; ok {
		t.Fatal("failed node handle remains registered")
	}
	if _, ok := orchestrator.mailboxes[6]; ok {
		t.Fatal("failed node mailbox remains registered")
	}

	replacementMailbox := controlplane.NewNodeMailbox()
	replacement := &nodeHandle{mailbox: replacementMailbox}
	orchestrator.nodes[6] = replacement
	orchestrator.mailboxes[6] = replacementMailbox

	orchestrator.removeExitedNode(6, old)

	if orchestrator.nodes[6] != replacement {
		t.Fatal("stale node cleanup removed a replacement handle")
	}
	if orchestrator.mailboxes[6] != replacementMailbox {
		t.Fatal("stale node cleanup removed a replacement mailbox")
	}
}
