package devices

import (
	"time"

	"github.com/codex-switch/admin-go/internal/platform"
	"github.com/google/uuid"
)

const accountUsageTimeout = 3 * time.Minute

type pendingAccountUsage struct {
	owner, device, request, account string
	client, desktop                 *peer
	timer                           *time.Timer
}

func validAccountUsage(message platform.JSON) bool {
	request, _ := message["requestId"].(string)
	device, _ := message["deviceId"].(string)
	account, _ := message["accountId"].(string)
	action, _ := message["action"].(string)
	return request != "" && len(request) <= 80 && deviceIDPattern.MatchString(device) &&
		account != "" && len(account) <= 160 && (action == "refresh" || action == "consume")
}

func (g *ControlGateway) requestAccountUsage(client *peer, session controlSession, message platform.JSON) {
	request, _ := message["requestId"].(string)
	fail := func(detail string) { sendAccountUsageResult(client, request, nil, detail) }
	if !validAccountUsage(message) {
		fail("账号请求无效，请重新选择账号。")
		return
	}
	device, err := g.service.owned(session.owner, message["deviceId"].(string))
	if err != nil {
		fail("无法访问这台电脑。")
		return
	}
	if checkCapability(device, commandSpec{capability: "account-usage"}) != nil {
		fail("请先更新电脑上的 Remote AI，再操作账号用量。")
		return
	}
	if err := g.service.available(session.owner, "account", message["accountId"].(string)); err != nil {
		fail("无法访问这个账号，请刷新账号列表。")
		return
	}
	g.forwardAccountUsage(client, session.owner, message)
}

func (g *ControlGateway) forwardAccountUsage(client *peer, owner string, message platform.JSON) {
	id, request := message["deviceId"].(string), message["requestId"].(string)
	account := message["accountId"].(string)
	g.mu.Lock()
	defer g.mu.Unlock()
	desktop := g.sockets[owner+":"+id]
	if desktop == nil || desktop.closed.Load() {
		sendAccountUsageResult(client, request, nil, "电脑已离线，请打开电脑端后重试。")
		return
	}
	count := 0
	for _, pending := range g.accountUsage {
		if pending.desktop != desktop {
			continue
		}
		count++
		if pending.account == account {
			sendAccountUsageResult(client, request, nil, "这个账号正在处理用量，请稍候。")
			return
		}
	}
	if count >= 4 {
		sendAccountUsageResult(client, request, nil, "电脑正在处理其他账号，请稍后重试。")
		return
	}
	command := uuid.NewString()
	timer := time.AfterFunc(accountUsageTimeout, func() { g.expireAccountUsage(command) })
	g.accountUsage[command] = pendingAccountUsage{owner, id, request, account, client, desktop, timer}
	desktop.send(platform.JSON{"type": "account-usage", "commandId": command,
		"accountId": account, "action": message["action"]}, nil)
}

func (g *ControlGateway) receiveAccountUsage(client *peer, session controlSession, message platform.JSON) {
	command, _ := message["commandId"].(string)
	g.mu.Lock()
	defer g.mu.Unlock()
	pending, ok := g.accountUsage[command]
	if !ok || pending.owner != session.owner || pending.device != session.device || pending.desktop != client {
		return
	}
	delete(g.accountUsage, command)
	pending.timer.Stop()
	// The PC sends only a usage summary, never account credentials or private details.
	sendAccountUsageResult(pending.client, pending.request, message["data"], "")
}

func (g *ControlGateway) expireAccountUsage(command string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	pending, ok := g.accountUsage[command]
	if !ok {
		return
	}
	delete(g.accountUsage, command)
	sendAccountUsageResult(pending.client, pending.request, nil,
		"电脑暂未确认结果，请先刷新用量，避免重复消耗。")
}

// Keep in-flight operations reserved when a browser disconnects to avoid duplicate consumption.
func (g *ControlGateway) disconnectAccountUsage(client *peer) {
	for command, pending := range g.accountUsage {
		if pending.desktop != client {
			continue
		}
		delete(g.accountUsage, command)
		pending.timer.Stop()
		sendAccountUsageResult(pending.client, pending.request, nil,
			"连接已断开，请先刷新用量，避免重复消耗。")
	}
}

func sendAccountUsageResult(client *peer, request string, data interface{}, detail string) {
	client.send(platform.JSON{"type": "account-usage-result", "requestId": request, "data": data, "error": detail}, nil)
}
