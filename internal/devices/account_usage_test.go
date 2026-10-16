package devices

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
)

func usageRequest(account string) platform.JSON {
	return platform.JSON{"deviceId": "device", "requestId": "usage-" + account,
		"accountId": account, "action": "consume"}
}

func TestAccountUsageValidatesInputAndAuthentication(t *testing.T) {
	for _, field := range []string{"requestId", "deviceId", "accountId", "action"} {
		message := usageRequest("account")
		message[field] = ""
		if validAccountUsage(message) {
			t.Fatalf("accepted missing %s", field)
		}
	}
	for _, value := range []interface{}{true, []string{"account"}, strings.Repeat("a", 161)} {
		message := usageRequest("account")
		message["accountId"] = value
		if validAccountUsage(message) {
			t.Fatal("accepted invalid account")
		}
	}
	valid := usageRequest("account")
	valid["deviceId"] = "12345678-1234-4123-8123-123456789012"
	if !validAccountUsage(valid) || !supportedCapability("account-usage") {
		t.Fatal("valid usage operation rejected")
	}
	g := newControlGateway(nil)
	client := queuedPeer()
	g.sessions[client] = controlSession{kind: "subscriber", expires: time.Now().Add(-time.Second)}
	if g.receive(client, platform.JSON{"type": "account-usage"}, nil) == nil {
		t.Fatal("expired session accepted")
	}
}

func TestAccountUsageRepliesRequireExactAuthenticatedComputer(t *testing.T) {
	g := newControlGateway(nil)
	caller, desktop, other := queuedPeer(), queuedPeer(), queuedPeer()
	g.sockets["owner:device"] = desktop
	g.forwardAccountUsage(caller, "intruder", usageRequest("account"))
	if updateFrame(t, caller)["error"] == "" || len(desktop.queue) != 0 {
		t.Fatal("cross-owner execution")
	}
	g.forwardAccountUsage(caller, "owner", usageRequest("account"))
	forwarded := updateFrame(t, desktop)
	if forwarded["type"] != "account-usage" || forwarded["action"] != "consume" {
		t.Fatal("wrong command forwarded")
	}
	reply := platform.JSON{"commandId": forwarded["commandId"], "data": platform.JSON{
		"consumed": true, "usage": platform.JSON{"primary": platform.JSON{"remainingPercent": 42}},
	}}
	for _, session := range []controlSession{{owner: "intruder", device: "device"}, {owner: "owner", device: "other"}} {
		g.receiveAccountUsage(desktop, session, reply)
	}
	g.receiveAccountUsage(other, controlSession{owner: "owner", device: "device"}, reply)
	if len(caller.queue) != 0 || len(g.accountUsage) != 1 {
		t.Fatal("forged reply accepted")
	}
	g.receiveAccountUsage(desktop, controlSession{owner: "owner", device: "device"}, reply)
	if updateFrame(t, caller)["requestId"] != "usage-account" || len(g.accountUsage) != 0 {
		t.Fatal("reply not routed to requester")
	}
	g.receiveAccountUsage(desktop, controlSession{owner: "owner", device: "device"}, reply)
	if len(caller.queue) != 0 {
		t.Fatal("duplicate reply delivered")
	}
}

func TestAccountUsageLimitsConcurrencyAndReservesDisconnectedConsumption(t *testing.T) {
	g := newControlGateway(nil)
	caller, desktop := queuedPeer(), queuedPeer()
	g.sockets["owner:device"] = desktop
	for index := 0; index < 4; index++ {
		g.forwardAccountUsage(caller, "owner", usageRequest(fmt.Sprint(index)))
		updateFrame(t, desktop)
	}
	for _, account := range []string{"0", "fifth"} {
		g.forwardAccountUsage(caller, "owner", usageRequest(account))
		if updateFrame(t, caller)["error"] == "" || len(desktop.queue) != 0 {
			t.Fatal("overlapping or excessive operation accepted")
		}
	}
	g.disconnectAccountUsage(caller)
	if len(g.accountUsage) != 4 {
		t.Fatal("browser disconnect released running consumption")
	}
	g.disconnectAccountUsage(desktop)
	if len(g.accountUsage) != 0 || len(caller.queue) != 4 {
		t.Fatal("PC disconnect did not notify callers")
	}
}

func TestAccountUsageTimeoutReportsUncertainOutcomeWithoutRetry(t *testing.T) {
	g := newControlGateway(nil)
	caller, desktop := queuedPeer(), queuedPeer()
	g.sockets["owner:device"] = desktop
	g.forwardAccountUsage(caller, "owner", usageRequest("account"))
	command := updateFrame(t, desktop)["commandId"].(string)
	g.accountUsage[command].timer.Stop()
	g.expireAccountUsage(command)
	message := updateFrame(t, caller)["error"].(string)
	if !strings.Contains(message, "避免重复消耗") || len(desktop.queue) != 0 || len(g.accountUsage) != 0 {
		t.Fatal("timeout retried consumption or failed to report uncertainty")
	}
}
