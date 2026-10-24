# admin-go 生产部署

生产后端只部署 `apps/admin-go`，管理页面与 Go 程序使用同一个镜像。
`apps/admin` 的 NestJS 源码已冻结，仅用于本地兼容对照测试。
以下命令从仓库根目录执行，不包含任何具体生产主机或凭据。

## 第三方模型高级设置同步（2026-10-15）

已有数据库先备份并执行 `sql/20261015-provider-request-options.sql`，再更新所有 admin-go 实例，
最后更新桌面客户端。该脚本为 `synced_providers` 增加可空的 `requestOptions` JSONB 列，
支持重复执行；空库初始化已包含该列。生产继续保持 `POSTGRES_DB_SYNCHRONIZE=false`。

高级设置与其他供应商字段独立记录修改时间。旧客户端省略该字段或发送 `null` 时保留已有值，
新版客户端发送空对象 `{}` 可清空设置。同步会检查自定义请求头、附加参数、大小与嵌套深度，
不允许覆盖认证、会话内容和协议控制字段。按模型保存的接口类型同时支持 Anthropic Messages。
验收时检查两台电脑的新增、修改、清空，以及旧客户端更新名称后高级设置仍保留。
本次复用原有 Provider 同步接口和 Kong 鉴权规则，无新增路由。回滚使用已验证的 admin-go 镜像，
保留新增列及数据，不执行删除列操作。

若上传返回 `fieldModifiedAt.property requestOptions should not exist`，说明服务端入口校验尚未支持新字段。
必须部署包含 Go Provider 同步契约扩展的镜像；仅执行数据库脚本不能解决此 400。
即使没有配置高级设置，新客户端也会上传该字段的修改时间。部署后验证单条和批量上传，
覆盖未配置、已配置和清空设置，并运行 `go -C apps/admin-go test ./internal/platform -run TestProviderSyncContract`。

## 官方模型列表缓存

Go 服务默认在启动后预取官方模型列表到 Redis，并每 30 分钟刷新一次。管理页面可关闭自动更新，
或将间隔设为 1–10080 分钟（最多 7 天）。关闭后启动时也不预取，仍可手动刷新或编辑列表。使用官方账户池中的凭据，
复用 Web 账户用量刷新的 `CODEX_OUTBOUND_PROXY` 出站代理配置；无需新增数据库表。
模型列表保存在 `official-models:catalog:v1`，没有过期时间，刷新失败、空列表或无效响应均保留旧值。
多实例通过 Redis 刷新锁和时间间隔标记避免重复读取数据库、重复请求上游。

管理页面“官方模型”支持查看、编辑完整模型列表及立即刷新。查看沿用 `admin.official-accounts.read`，
编辑和刷新沿用 `admin.official-accounts.manage`；仅有个人账户池权限不能修改全局列表。
手动保存直接更新 Redis；开启自动更新时，按已设置的间隔重新计时，关闭时不会恢复自动更新。
自动更新设置保存在 `official-models:settings:v1`，无过期时间，由所有 Go 实例共用；
`GET/PATCH /admin/api/official-models/settings` 使用原有查看／管理权限，更新时检查设置版本并记录审计。
保存设置后重新计时，后台每分钟检查一次；版本检查防止慢刷新覆盖手动修改或设置变更前的任务写回。
上线时需更新所有 Go 实例，旧版本不识别关闭开关。无需数据库升级脚本。
Redis 应启用持久化，清空 Redis 后需重新预取或手动录入。

公共 `GET /official-models` 只读取 Redis，不查询数据库或请求上游，也不返回账户凭据。
使用 Kong 时将 `/official-models` 加入 Go 服务的公共路由（示例配置已更新）。缓存尚未就绪或 Redis
不可用时返回 503；新版客户端的对外代理和 Codex GUI 优先向官方获取模型，仅在官方请求失败时读取此缓存。
先部署 Go 服务与管理页面，再更新客户端。验收时检查缓存命中、自定义间隔、关闭及重新开启、手动编辑、空响应保留旧值、
出站代理和客户端回退。测试可设置 `MODEL_CATALOG_TEST_REDIS` 指向隔离的 Redis 后运行 accounts 测试。

## 电脑与手机更新包共享

支持 `update-peer-*` 消息的 Go 服务允许登录中的电脑跨账号匹配同一更新包；
版本、下载地址与签名仍由电脑从 GitHub 查询。`/device-switch` 只传递更新包标识和临时直连配置，
不传安装包、不开放其他账号的设备操作权限。全局下载设置需要先执行
`sql/20261015-update-sharing.sql`；空库初始化已包含该表，生产保持 `POSTGRES_DB_SYNCHRONIZE=false`。

复用现有 `CHAT_NATIVE_PEERS`、`CHAT_NATIVE_STUN` 和禁止数据中转的直连协调节点。
先更新 Go 服务，再更新电脑客户端；未配置直连节点、旧服务、无法直连或校验失败时自动回退 GitHub。
匹配表、上传名额和短期失败记录保存在现有 Redis 中，各 Go 实例可共同匹配在线电脑。
Redis 只保存短期记录和转发连接配置，不传安装包；Redis 暂不可用时拒绝新匹配并回退官方来源。
旧版电脑继续按原来的 GitHub 更新流程运行。回滚使用已经验证的 admin-go 镜像。

新版允许有效登录的手机通过订阅连接申请 APK 下载，但不允许手机发布种子。
一次请求最多匹配三台支持范围传输的电脑，优先自己账号，并为每路分配独立会话；
新版使用 512 KiB 小块按需分配任务；快节点可接替慢节点，单路失败不会丢弃已完成的块。
GitHub 在两秒宽限后参与范围下载，避免等待一个慢节点完成整包；不支持范围请求的源仍可走原整包回退。
已完成的块写入私有断点文件并保存摘要，重试时重新校验后复用；这些文件不会对其他设备共享。
只有完整安装包通过原有签名或官方摘要校验后，才进入共享缓存。所有来源失败或整包校验失败时保留官方回退。
电脑默认额外缓存一份官方 Android APK，可在管理后台“下载优化”中关闭提前准备。
部署顺序为 Go 后端、电脑客户端、Android 客户端；手机只从 GitHub 获取版本与摘要，
手机自身自动下载仍限制在空闲且使用 Wi-Fi 时，手动下载允许移动网络。服务器官方文件副本配置见本文末节。

先更新所有 Go 实例，再更新桌面和 Android；旧客户端继续使用旧协议，新客户端协商二进制分块协议。
共享配置统一由管理后台“下载优化”控制，桌面设置页不再显示共享卡片，也不读取或迁移旧本地配置。
默认范围为所有用户、上传不限速、允许计费网络上传、开启 Android 包提前准备。
后台可选择关闭、我的设备或所有用户，上传速度可设为 0（不限）或 64–8192 KiB/s，两路上传共用预算。
Android 接收端默认允许跨账号匹配；Go 在本机和 Redis 匹配前统一执行后台的范围限制。
开启“仅在非计费网络上传”后，Windows 使用系统网络计费信息、Linux 使用 NetworkManager；
无法确认网络费用时暂停上传。公开跨账号共享会让连接双方获知对方的网络地址。

`GET /update-sharing/settings` 公开提供无敏感信息的策略，Kong 需加入同名公共路由。
`GET/PATCH /admin/api/update-sharing` 分别要求 `admin.update-sharing.read`、`admin.update-sharing.manage`，
保存与审计同一事务提交；内置管理员启动时自动获得新权限，自定义角色需分配相应权限。
桌面通过配置的 HTTPS 服务异步读取，每 30 秒刷新一次且不重叠；刷新失败保留内存中的有效设置，
初次运行使用上述默认值。服务端读取策略失败时拒绝新共享匹配。回滚保留设置表和数据。
已验证缓存最多 1 GiB、保留原有各类包数量上限；断点区另外最多保留两份未完成包，每份不超过 512 MiB。
短暂控制连接重连不取消新版直连传输；退出账号或关闭共享仍会关闭本机传输，所有连接凭据最长有效 20 分钟。
失败反馈仅影响报告者后续五分钟的节点选择，不能借此封禁其他用户的节点。
共享传输不记录对端账号、网络地址或密钥。

官方版本信息仍来自 GitHub；首次发现一个版本需要官方元数据。分块摘要用于检测块损坏，
不能代替发布者签名，最终整包校验始终保留。浏览器继续使用站点资源预加载，不启用原生安装包传输。
跨实例回归可使用隔离 Redis，设置 `UPDATE_PEER_TEST_REDIS=127.0.0.1:端口` 后执行
`go test ./internal/devices -run 'Update(Cluster|Peers)'`；测试不清空 Redis 数据库。

## 数据库连接与响应时间（2026-10-10）

单个 admin-go 实例默认最多使用 30 个 PostgreSQL 连接（此前为 10），保留最多 10 个空闲连接。
可通过 `.env` 的 `POSTGRES_MAX_OPEN_CONNS`、`POSTGRES_MAX_IDLE_CONNS` 调整；空闲数不能超过上限，
连接上限必须大于零。空闲超过 5 分钟的连接会回收，连接最长使用 1 小时。
多实例部署时，应将所有实例的连接上限合计后，与数据库 `max_connections` 比较，并为运维和其他服务预留连接。

`POSTGRES_STATEMENT_TIMEOUT_MS` 默认 30000，限制单条 SQL 执行时间；
`POSTGRES_LOCK_TIMEOUT_MS` 默认 5000，限制数据库锁等待时间，两者均须大于零。
登录、令牌刷新、身份查询和官方用量读写同时设置 30 秒操作期限，包含连接池排队时间，并响应客户端取消。
登录及刷新令牌的权限查询复用当前事务，不再额外占用连接。
这些设置随 Go 镜像更新生效，无需新增数据库升级脚本；部署后可按实例数量和实际数据库容量调整。

官方用量按变化批量写入；重复上报不重算，分钟明细变化按差额更新当前额度阶段。
新增额度观察只读取当前阶段，修正历史观察或阶段超出保留期时才重新检查保留范围内的历史。
完整分钟的消耗使用已保存的合计，汇总接口固定执行 3 次查询，避免账号数增加时逐个查询。

## 官方账户跨设备用量（2026-10-09）

先执行 `sql/20261009-official-usage-v2.sql`，再更新 admin-go 和桌面客户端。
新版使用独立的 `official_usage_v2_*` / `official_quota_v2_*` 表，不导入旧版统计记录。
接口统一使用 `/official-usage/v2`，上报必须包含 `version: 2`；新旧客户端不会混用统计。
旧表保留供回滚使用。需要同时更新 Go 服务和桌面客户端，新客户端不会向旧接口补传数据。
Kong 受保护路由增加 `/official-usage`，保持 `strip_path: false`；空库会自动创建用量表。
桌面登录后每分钟自动向 `POST /official-usage/v2/records` 上报官方账户用量，无需打开统计页，
也不依赖账户同步开关。其他设备的明细不会下载到本机；第三方 Provider 不参与。
客户端首次运行新版时保存统计起始时间，之后只分批上传此后的记录，最多保留最近 53 周。
未登录或首次上传失败也不会推迟起始时间；旧版的上报进度与历史额度点不参与新统计。
后续只上传变化的分钟桶和百分比变化或重置的观察。
服务器在接收上报时清理该账户超过 53 周的分钟明细和观察，保留精简的历史下降阶段汇总。
同秒请求合并为 `[秒偏移, Token 总数, USD 消耗]`，每分钟一组；不上传请求 ID、模型、对话内容或凭据。
分钟桶按登录用户、设备、账户和分钟覆盖写入，重试不会累加，延迟完成的请求可以补记。
本机仅保存已确认桶的摘要和最后观察作为上报进度；失败重试不会跳过未确认的记录。
服务器将额度下降阶段的起止时间、起止百分比、下降百分点和跨设备美元消耗持久化，
相同时间的重复观察只计一次；只有实际观察到额度百分比回升才重新确定起点。
额度持平、缺失中间观察或重置时间变化均保留原起始时间与百分比；最新观察冲突或过期时暂停估算。
100% 额度 = 起点之后各设备累计消耗 /（初始百分比 − 当前百分比）× 100；
当前可用额度 = 100% 额度 × 当前剩余百分比 / 100。
最后观察之后的新消耗也计入累计成本，不另行扣减。百分比尚未下降时不估算金额。
两种额度分别估算，可用额度取较小值，缺少有效样本时不估算。
`GET /official-usage/v2/summary?startTs=...` 仅返回账户及设备汇总、完整额度和可用额度估算。
手机和 Web 的汇总由所连接电脑读取此结果。旧服务不支持接口时显示暂时不可用。
美元估算沿用各设备现有 Token 计价设置，并非官方余额；未记录或尚未上报的消耗无法纳入。
回滚使用已验证的 Go 镜像并保留新增表；无需更改 NestJS。

## 文件下载数据面与独立计费开关（2026-10-05）

升级前应用 `sql/20261005-chat-bulk-leases.sql`，即使暂不开启批量计费，也需要该表以保证
原计费路径识别未结算的下载额度。新安装的空库会自动创建该表。生产仍使用手动增量迁移。
必须先停止旧 Go 实例接入再切换；不识别新预留表的版本不能与新版混合承接计费转发。

管理端「聊天设置 → 文件下载 → 文件校验下载（试用）」对应 `fileBulkEnabled=0/1`，默认关闭。
仅双方及当前路径支持二进制协议时，新任务才使用 bulk。旧客户端继续兼容模式。
关闭后停止新任务使用 bulk；已有任务在下一次控制请求时停止，并从已校验断点重新协商。

`CHAT_BULK_LEASES_ENABLED=false` 是独立的服务端开关。保持关闭时，bulk 继续调用原有
`Meter.Transmit`。开启后，writer 只对已排队、已通过授权与限速的记录申请一次性字节租约：
接口上限 1 MiB，当前队列将每批进一步限制为 256 KiB、最多 16 条记录；不为凑批等待。
每条 socket 写入前检查过期与连接状态，并在记录间优先处理控制消息。

下载突发填满队列时，接收循环等待 writer 释放空间，向发送端传播 TCP 背压；不再因
一次正常突发直接断连。连接、账户、实例的队列上限仍分别为 256 KiB、2 MiB、32 MiB，
容量包含转发封装。单条记录最多等待 5 秒，源端或接收端关闭会立即结束等待。
等待期间不持有全局会话锁，其他设备及控制消息可以继续处理。

租约绑定账号、电脑、计费时段、唯一租约 ID、唯一 writer 和额度 epoch。PostgreSQL 原子预留，
Redis 确认额度 fence；只允许一次 writer claim，不接管或重新使用崩溃进程的租约。
租约最多有效 2 秒，已获准开始的 socket 写入允许结束。账户最多 4 个活动租约、64 个未解决租约；
额度不足或账本失败时停止发送，不能先发后补。原有成功 socket 写入的封装字节口径保持一致，
不把浏览器提交点或 transport credit 当作计费依据，也不声称 socket 成功代表对端已收到文件。

最终结算使用单调累计成功字节数，重复结算不重复收费。能证明未尝试发送的部分才释放；
写入失败、丢失结算响应或进程崩溃留下的未知部分保持 `reserved`，到期只转为 `uncertain`。
原 writer 在到期后仍可提交一次最终结果；持久化 claim 和 receipt 标记区分过期占用与已结算结果，
不会因此重新授权发送或重复结算。没有最终结果时继续占用额度。
未知额度不自动退款，也不自动伪装成成功流量；`chat_relay_bulk_leases` 保留账号、writer、
成功字节数和占用字节供审计。终态记录保留 30 天，未知记录必须在核对持久化证据后人工处理。

回退计费只需关闭 `CHAT_BULK_LEASES_ENABLED` 并滚动重启本版本，原逐帧路径会继续扣除
尚未解决的租约占用。回滚到不认识此表的旧 Go 镜像之前，必须停止新租约并完成全部未知额度对账，
确认不存在 `reserved>0`，再使用已验证镜像；不要删除记录、清空 Redis 或用 TTL 模拟退款。
本轮没有开启生产开关或部署。目标设备内存、弱网、RPC P95 与完整下载耗时门槛见
[下载管理与验收](../../docs/mobile-download-manager.md)，未满足门槛前保持两个开关关闭。

## 手机推送与 Windows 无人值守（2026-09-28）

已有数据库先备份，再执行 `sql/20260928-chat-push.sql` 和
`sql/20260928-desktop-service.sql`，然后更新 Go 镜像。两项功能默认关闭。
推送使用 Expo 转发 APNs；服务器设置 `CHAT_PUSH_ENABLED=true`，按项目配置
`EXPO_PUSH_ACCESS_TOKEN`。移动构建设置和真机验收步骤见
[远程可靠性说明](../../docs/remote-reliability.md)。禁止将访问令牌放进客户端构建变量。

Windows 无人值守设置 `DESKTOP_SERVICE_ENABLED=true`，并更新桌面、手机和 Web。
普通账号认证的 `/devices/:deviceId/service-credential` 用于颁发和撤销设备授权；
`/desktop-service/revoke` 只接受服务自己的设备凭据，不能套用 Kong 的账号 JWT 插件。
Kong 新增受保护路径 `/chat-push`，新增直达 Go 的 `/desktop-service/revoke`；
已有 `/device-chat`、`/device-switch` 的 WebSocket 升级和超时设置继续保留。
参考 `kong/existing-kong.example.yml` 增量调整现有路由，不覆盖整份生产配置。

凭据只允许指定账号的指定电脑作为远程宿主上线，不授权账号 HTTP 接口或手机查看端。
轮换、设备菜单撤销或服务主动注销会拒绝旧凭据的新连接，并关闭该实例上的旧服务连接。
多实例部署尚未加入跨实例撤销广播，其他实例上的已有连接最多保留到一小时授权到期；
配置即时撤销要求时先使用单个协调实例。断网时媒体访问也受到本地授权截止时间约束。
删除离线设备时同时删除其服务凭据，重新登记同一设备不会恢复旧授权。

回滚仅使用已验证的 Go 镜像并保留新增表。先关闭以上开关、撤销服务授权并在电脑上停用服务。
服务端推送队列会保留未完成记录，回滚期间不发送；不要删除队列来模拟成功投递。
Windows 安装、锁屏/登录验收与限制见 [无人值守说明](../../docs/windows-unattended.md)。

## 设备上报防刷（2026-09-24）

本次更新无需 SQL 迁移。部署前将 `TRUSTED_PROXY_CIDRS` 配置为实际入口代理地址的
明确 `/32` 或 `/128`，保留现有环境变量；默认不信任任何转发头。代理地址改变时必须同步更新。
默认请求与新增安装限额、兼容变化、验证方式见 [TELEMETRY.md](TELEMETRY.md)。
仅更新 Go 镜像即可启用服务端保护；桌面测试隔离在更新代码后生效。

## 远程聊天转发升级（2026-09-23）

启动新版 Go 服务前应用 `sql/20260923-chat-relay-budgets.sql`。切换时停止旧 Go
实例，再启动新版；旧版计费不识别预留额度，不能和新版同时承接转发。
客户端可以逐步更新：二进制转发按每条 WebSocket 连接协商，旧客户端继续使用 JSON。

转发额度由 PostgreSQL 持久化预留，每份最多 256 KiB、有效期最多 5 秒，并在整点切换。
Redis 原子扣减和确认每帧用量；网络写入期间不持有数据库或分布式锁。
后台每 5 秒结算过期预留，正常报表可能延迟约 5–10 秒。额度下调会撤销尚未使用的旧预留，
已开始发送的帧允许完成。多实例共用同一 PostgreSQL 和 Redis；临近额度上限时，其他实例
可能短暂等待已预留但尚未使用的额度释放。

Redis 故障时转发停止放行，P2P 不受此计费路径影响；不能退回仅检查数据库历史用量。
保留 Redis AOF 和持久卷，避免把此处的 Redis 键作为普通缓存清理。重启后后台会恢复结算
过期预留；仍有未确认发送的预留等待至到期后一分钟，再按已确认及结果不明的部分计费。
如果整份 Redis 记录丢失，或者 Redis 重启、主从切换导致记录属于旧进程，按该份数据库预留
全额计入，防止持久化回退后重复发放额度；这可能保守多计
该份尚未使用的流量（每份最多 256 KiB）。额度修改过程中发生存储故障时会保留阻断状态，
修复存储后重新保存该用户额度即可恢复。
如配置 Redis ACL，需要允许脚本读取 `INFO server`，用于确认记录所属的 Redis 进程。

若需回滚，仅使用已验证的 Go 镜像：停止新版接入，保持 Redis 可用并等待所有
`chat_relay_budgets` 未结算记录完成（含未确认发送的恢复期），确认没有 `closed=false`
记录后再启动旧 Go 实例。保留新增表，不要删除未结算预留。

## 已有环境与新安装

已有环境沿用 PostgreSQL、Redis、Kong、数据库目录和密钥。记录当前 Go 镜像 ID、Compose
项目名、所有 Compose 文件、网络、端口与数据容器的 ID/启动时间。保留服务器上的 `.env`
和 `compose.override.yml`；不要用示例覆盖现有配置。正常更新只操作 `admin-go`。

新安装先复制 `apps/admin-go/.env.example` 为同目录的 `.env`，设置真实数据库密码，
为 `KONG_JWT_SECRET`、`JWT_REFRESH_SECRET` 分别生成随机密钥，并限制文件访问权限。
`KONG_JWT_KEY` 与 Kong JWT credential 的 key 保持一致；已有安装的三个值必须保持原样。
管理配置中的 SMTP 等密文也依赖原密钥，不能在升级时重新生成。

`ADMIN_GO_DB_NETWORK`、`ADMIN_GO_KONG_NETWORK` 必须对应实际 Docker 网络。
已有数据库网络即使仍叫 `admin_backend-internal` 也应保留，不要因为迁移目录而重建。
`POSTGRES_HOST` 和 `REDIS_HOST` 填写各自在该网络上的地址；保留实际出站代理
`CODEX_OUTBOUND_PROXY`，有意直连时留空。Go 容器已提供 `host.docker.internal` 映射。

仅在全新安装、尚无数据服务时，创建指定的数据网络并启动可选依赖：

```sh
docker network create codex-switch-data
sudo install -d -m 0750 /srv/codex-switch/postgres
docker compose --project-name codex-switch-data --env-file apps/admin-go/.env \
  -f apps/admin-go/compose.infrastructure.yml up -d postgres redis
```

这份文件不运行 NestJS，也不启动 Go 或 Kong。PostgreSQL 使用配置的宿主机目录，
Redis 使用持久卷。已有数据服务不执行这组新装命令。Kong 网络由现有网关管理。

## 本地验证、构建和打包

先确认目标提交已提交且工作区干净；存在其他改动时使用独立检出目录。
运行 `npm run check:backend`、`npm run check -w @codex-switch/admin-ui`，
然后执行 Docker 的 `verify` 阶段（Linux race tests 与 vet）。
首次迁移或修改认证、数据格式、PC/手机协议时，还需运行 [README](README.md) 中的隔离对照测试。

构建平台必须匹配服务器。以下 PowerShell 示例假设服务器为 `linux/amd64`：

```powershell
$targetCommit = (git rev-parse HEAD).Trim()
$imageTag = "codex-switch/admin-go:$targetCommit"
docker build --platform linux/amd64 -f apps/admin-go/Dockerfile --target verify -t codex-admin-go:verify .
if ($LASTEXITCODE -ne 0) { throw 'Go container verification failed' }
docker build --platform linux/amd64 -f apps/admin-go/Dockerfile -t $imageTag .
if ($LASTEXITCODE -ne 0) { throw 'Production image build failed' }
$imageId = (docker image inspect $imageTag --format '{{.Id}}').Trim()
if ($LASTEXITCODE -ne 0) { throw 'Image inspection failed' }
$archive = Join-Path ([IO.Path]::GetTempPath()) "admin-go-$targetCommit.tar"
docker image save --output $archive $imageTag
if ($LASTEXITCODE -ne 0) { throw 'Image archive failed' }
Get-FileHash -LiteralPath $archive -Algorithm SHA256
```

记录提交、平台、镜像 tag/ID 与文件 SHA-256，用 `scp` 上传到服务器受保护的部署记录目录。
使用文件传输，不把二进制镜像管道接入 PowerShell 文本处理。镜像中不包含生产 `.env`。
下载源不通时可使用 `GOPROXY`、`NPM_REGISTRY` 构建参数，不修改代码或关闭依赖校验。
服务器网络命令应遵循该环境已配置的代理要求。

## 数据库与配置

升级前备份 PostgreSQL 和 Redis，并确认备份可读取。数据库升级脚本位于 `sql/`，
只有确认缺失且兼容的升级才按版本顺序执行；目录移动不代表需要重新运行已应用的 SQL。
生产保持 `POSTGRES_DB_SYNCHRONIZE=false`。它不是增量迁移开关，不能升级已有数据库。

设备管理分别切换代理接口与 Codex GUI 模型需要先执行
`sql/20260924-device-gui-model-selection.sql`，再更新 admin-go、Web/移动端及桌面端。
新增的 `guiAccountId`、`guiProviderId` 字段仅保存 GUI 选择，不修改代理接口当前配置。
旧桌面端仍支持原有代理切换，GUI 切换会提示更新；回滚 Go 镜像时保留新增字段即可。

模型计价预设需要 `sql/20260923-token-cost-presets.sql`。已有库确认缺少
`token_cost_preset_settings` 时，在备份后执行该增量脚本；它只新增配置表，不修改用户数据。

密码登录锁定需要 `sql/20260923-user-login-locks.sql`。更新应用前，在备份后为已有库执行该脚本，
新增 `user_login_locks` 表以保存账号的连续错误次数和锁定截止时间。脚本可重复执行，不修改已有用户。
应用镜像回滚时保留该表即可；旧镜像不会执行新的登录锁定规则。

个人聊天流量和每月额度需要 `sql/20260923-chat-user-traffic.sql`。先备份并执行脚本，
新增用户额度、月用量和小时用量表，再更新 admin-go 与管理页面。默认额度为 `-1`（不限量），
每月按北京时间自动切换统计周期。个人历史从升级后开始记录；旧的全站统计无法拆分到用户。
内置管理员自动获得新权限，自定义管理角色需授予 `admin.chat-traffic.read` 和按需授予
`admin.chat-traffic.manage`。旧 Go 镜像可以保留新增表回滚，但不会执行新额度限制。

全新空数据库第一次启动可以临时在 `.env` 设置 `POSTGRES_DB_SYNCHRONIZE=true`，
由 Go 初始化完整结构；初始化成功后改回 `false`，仅重建 Go 服务。
不要将 `internal/migrations/001_legacy_schema.sql` 直接导入已有库。

HTTP 默认只发布 `127.0.0.1:8080`，Kong 通过容器网络访问 Go。保留实际生产别名和覆盖文件。
内置 STUN 默认监听并发布 UDP 3478；`CHAT_STUN_URLS=stun:你的公网域名:3478`
需要匹配公网映射、安全组和防火墙。UDP 不经过 Kong。
可选 TCP 打洞发现服务使用 `CHAT_TCP_URLS=tcp://你的公网域名:3478`，
并设置 `CHAT_TCP_LISTEN=0.0.0.0:3478`，开放公网 TCP 3478。
Compose 使用 `ADMIN_GO_TCP_BIND` / `ADMIN_GO_TCP_PORT` 控制宿主机映射。
`CHAT_TCP_URLS` 留空时不启动 TCP 服务，也不向客户端启用新路径；映射端口仍由 Compose 预留，
若该 TCP 端口已有其他服务，需调整映射或在覆盖文件中删除它。
可用逗号填写最多两个发现地址；第二个应位于另一公网地址以提供冗余。
IPv6 需使用 `tcp://[公网IPv6]:3478` 并配置对应 IPv6 监听、容器路由及防火墙。
发现服务必须直接接收 TCP 连接，不能放在会改写来源地址的 HTTP 反向代理后面。
它只返回连接来源地址，不中转聊天内容；远程桌面视频仍使用原有 ICE/TURN。
当前新增 TCP 路径支持桌面与 Android；Web、iOS 和旧客户端继续使用现有 WebRTC/中转。
PC 发起端和被控端都需要带 TCP 协商的版本；仅更新被控端不会为旧 PC 发起端启用 TCP。
服务端在会话恢复时重新下发已协商的 TCP 发现配置，原生层据此恢复目标地址权限。
如果客户端网络把发现域名解析为 `198.18.0.0/15` 等代理假 IP，应优先使用运维确认的固定公网 IP
配置 STUN 与 TCP 发现地址，例如 `stun:公网IPv4:3478` 和 `tcp://公网IPv4:3478`。
使用同一本地 socket 对比域名与实际 IP 的映射，再从公网验证 UDP/TCP 发现回应。
字面 IP 可以消除该域名解析路径的干扰，但不能保证跨 NAT 打洞成功，也不绕过客户端网络策略。
使用外部 STUN 或设置 `CHAT_STUN_PORT=0` 时，应在覆盖文件中用 `ports: !override`
移除基础 UDP 映射，避免它指向端口 0；按需保留 HTTP 和 TCP 发现端口，此语法需要 Compose 2.24.4+。

## 加载并更新 Go

仓库根目录提供 `npm run deploy:admin` 和 `npm run deploy:web`，分别更新管理后端（含管理页面）
和独立 Web。命令在当前 Docker 环境执行，要求 Node.js、Docker Compose；admin 还需要 Python 3。
它们使用已构建或加载的镜像，不执行 SSH、上传、数据库迁移或镜像构建。
先完成前文的目标提交验证、镜像加载、回滚镜像保留和数据库检查，再运行更新命令。

```bash
# 替换为当前容器记录的实际 Compose 项目名。
npm run deploy:admin -- check --project-name admin-go
npm run deploy:admin -- up --project-name admin-go
npm run deploy:admin -- verify --project-name admin-go

npm run deploy:web -- check --project-name web
npm run deploy:web -- up --project-name web
npm run deploy:web -- verify --project-name web
```

省略操作名时默认执行 `up`；`check` 和 `verify` 都是只读操作。
admin 命令直接调用下面的 Python 入口，保留其全部配置和中继保护。
Web 必须显式传入 `--project-name`，已有环境沿用原项目名，避免另起一份服务。
两者都支持 `--env-file`、可重复的 `--file`，以及仅用于 `up` 的 `--force-recreate`。
Web 默认读取已有 `apps/web/.env`；使用其他变量文件或运行时环境变量的部署，后续命令应保持一致。
Web 保留容器记录的完整 Compose 文件顺序，新安装读取基础配置和已存在的 `compose.override.yml`。
Web 更新只操作 `web`，不构建、不拉取镜像、不重启依赖，等待健康检查并核对镜像、网络和访问别名。
命令成功后仍须验证公网页面、静态资源、API 和实际连接；admin 的中继验收要求见下文。

服务器仓库必须能快进到已验证的目标提交，禁止 reset 或强制覆盖本地改动。
先记录运行镜像并创建回滚 tag，再校验上传文件的 SHA-256，使用 `docker image load --input`
加载镜像，并核对镜像 ID、Linux 平台与本地记录一致。
`ADMIN_GO_IMAGE` 可指定提交 tag；未设置时沿用 `codex-switch/admin-go:local`。
如沿用原镜像引用，将已验证的新镜像 ID 标记到该引用；保留单独的回滚 tag。

更新和回滚统一使用下面的入口（需要 Python 3 和 Docker Compose）。使用实际观察到的项目名：

```bash
set -Eeuo pipefail
python3 -B apps/admin-go/scripts/deployment_compose.py check --project-name admin-go
python3 -B apps/admin-go/scripts/deployment_compose.py up --project-name admin-go
python3 -B apps/admin-go/scripts/deployment_compose.py verify --project-name admin-go
```

入口从当前容器的 Compose 标签保留完整文件列表和顺序，包括私有覆盖文件及其他可选服务配置。
新安装默认加载 `compose.yml` 和已存在的 `compose.override.yml`。目标配置或当前服务启用了
`DESKTOP_TURN_URLS` 时，自动补上 `compose.desktop-relay.yml`；未启用的环境不会自动开启中继。
额外覆盖文件用 `--file 路径` 追加；`--env-file 路径` 指定 Compose 变量替换文件，
服务自身的 `env_file` 路径仍由 Compose 配置决定，使用其他文件时需在覆盖配置中同步设置。

`check` 只读检查，不打印展开的环境变量。缺失已使用的配置文件、意外关闭已启用的中继、
缺少已公布的 UDP/TCP/TLS 端口映射、映射仅绑定回环地址、中继网络断开或暴露私有 TURN 认证端口，
都会阻止 `up`。`up` 只执行 `admin-go` 的 `--no-deps --no-build --pull never` 更新，随后检查实际容器；
它不会创建或启动 TURN 后端，新装中继仍按 [DESKTOP-RELAY.md](DESKTOP-RELAY.md) 单独准备。
`verify` 核对实际运行状态、端口和共享网络；公网连通性和真实视频连接仍需完成下文的验收。

本地回归检查：`python -B -m unittest discover -s apps/admin-go/scripts -p 'test_deployment_*.py' -v`。
设置 `COMPOSE_CONFIG_TEST=1` 会额外使用临时配置验证真实 Compose 合并，无需启动容器或访问生产。

不要在加载本地镜像后重新构建，不运行整栈 `down` 或 `--remove-orphans`。
更新 Go 不需要重启 PostgreSQL、Redis、Kong 或 Web。数据库和 Redis 的 ID/启动时间应保持不变。

独立 Web 的入口在 `apps/web/compose.yml`。需要更新 Web 时另行构建、上传并使用原 Web
项目名更新 `web`；已有 Web 容器不能改用新项目名再启动一份。
全新 Web 安装可选择独立项目名。它不依赖或启动 NestJS，默认网络别名为 `codex-switch-web`。

## Kong

新网关参考 [existing-kong.example.yml](kong/existing-kong.example.yml)，
Go 上游为 `http://codex-switch-admin-go:8080`，Web 上游为 `http://codex-switch-web:80`。
自定义 `ADMIN_GO_KONG_ALIAS` 时使用实际别名。已有 Kong service 的名称和 ID 可以保留，
只修改必要的上游地址；不要覆盖其他路由、插件、TLS 或超时设置。
DB-backed Kong 通过已确认的 Admin API 管理；声明式 Kong 应持久化修改原配置。

`/auth`、`/admin` 等公开路径和受保护的 `/sync`、`/devices`、`/admin/api` 都指向同一 Go 进程。
公开路由还需包含 `/token-cost-presets`，供 PC 启动时读取计价预设；保持 `strip_path=false`。
`/help-assistant/settings` 也属于公共路由，供客户端读取帮助助手设置。
`/device-switch`、`/device-chat` 必须保留 WebSocket Upgrade；首帧在 Go 内校验 JWT。
JWT 插件使用 `key_claim_name=iss`、`claims_to_verify=exp`、`run_on_preflight=false`。
`/web` 指向独立 Web 服务并启用 `strip_path=true`。

新增接口时同步维护示例配置，避免仅在线上添加路由。运行
`go -C apps/admin-go test ./internal/server -run TestKong -count=1`，
将 Go 实际注册的全部接口与示例逐一对照，同时检查上游、路径保留和主要鉴权边界。
此项检查已包含在 Go 测试和部署配置 CI 中；部署时仍需核对线上配置与示例的差异。

Kong 的 worker 配置和 DNS 缓存可能晚于修改生效。重建容器后应核对新 IP，并在限定时间内
重复公网检查；不能仅凭 Admin API 返回成功判断流量已切换。若需更换别名，应先在持久化
Compose 配置中加入并验证新别名，再更新相应上游。不要为了刷新缓存重启整个网关。
Go 为兼容旧接口保留了 `X-Powered-By: Express`，该响应头不能判断实际运行语言。

## 验收与回滚

启用远程桌面视频中继时，按 [DESKTOP-RELAY.md](DESKTOP-RELAY.md) 追加 Compose 文件并验证视频与控制。
以后每次更新、回滚都要带上同一份中继 Compose 文件，保留私有环境变量、端口和网络。

- 容器使用预期镜像且持续运行，无重启循环；同时读取 stdout/stderr，确认 `admin-go listening`。
- 本机和公网 `/admin`、页面静态资源返回 200；未登录 `/auth/me` 返回 401。
- 使用专用测试账号和模拟设备验证两个公网 WebSocket、设备指令确认、PC/手机配对、
  双向加密转发、手机恢复和桌面会话重建，不向真实用户的 PC 发送测试指令。
- 从服务器外部发起 UDP STUN Binding 请求。HTTP 正常不能证明公网 UDP 可达。
- 验证数据库、Redis 未被重建或重启；删除临时测试账号并保存部署记录。

出现确认的更新故障时，恢复已记录的上一版 admin-go 镜像及其兼容配置，使用同一 Compose
项目和文件执行 `python3 -B apps/admin-go/scripts/deployment_compose.py up --project-name admin-go --force-recreate`，
再执行相同验收。回滚只使用 Go 镜像，不启动 NestJS；不自动恢复数据库备份覆盖在线数据。
首次迁移在旧服务停止前完成并行验证；缺少已验证的 Go 回滚镜像时应在切换前解决这一条件。

## PC 远程协助

PC 本地模式的远程协助需要先部署包含 `/remote-assistance` 接口的 admin-go，再更新双方 PC 客户端。
使用 Kong 时，将 `/remote-assistance` 加到指向 Go 服务的受保护路由，保留 JWT 校验且不剥离路径。
无需数据库升级。发起请求统一返回等待状态，不区分邮箱是否注册、账号是否禁用、对方是否在线或收件箱是否已满。
存在可接收的账号时，将邀请送到该账号已登录且支持协助的桌面客户端；无人值守服务不能接受邀请。
邀请 5 分钟过期，接受后最长协助 2 小时。邀请及授权只保存在当前协调服务内存中，服务重启后需要重新邀请。

验收使用专用测试账号：检查存在/不存在/离线/禁用邮箱的请求响应一致、接收邀请、接受/拒绝、
桌面画面和键鼠操作、发起方取消/结束、
接收方结束、邀请过期、退出登录与网络重连。协助连接不能读取聊天历史、账号列表或调用终端/聊天操作；
取消后不能继续操作，也不能借用原会话恢复连接。发起方断网时仍应能立即在本机结束控制。
旧客户端保持原有同账号远程功能，不能接受跨账号协助。回滚使用已验证的 admin-go 镜像，回滚后不保留邀请。

## 聊天原生直连节点

聊天直连可增加 EasyTier 用户态内核；配置未开启或任一端为旧客户端时，继续使用原有链路。
需要先更新桌面和手机原生包，再配置 Go 服务。Web 浏览器继续使用 WebRTC。

使用 `compose.chat-connectivity.yml` 作为现有 Go Compose 栈的附加文件，构建上下文为仓库根目录。
节点源码固定在 `crates/chat-connectivity/Cargo.lock` 对应的版本；不要改用不固定版本的公共节点镜像。

```dotenv
CHAT_NATIVE_PEERS=udp://p2p.example.com:11010,tcp://p2p.example.com:11010
CHAT_NATIVE_STUN=stun-a.example.com:3478,stun-b.example.com:3478
CHAT_RENDEZVOUS_SECRET=replace-with-a-separate-random-secret-at-least-32-characters
```

`CHAT_NATIVE_PEERS` 最多四个自有节点地址。开放节点的 11010/TCP 和 11010/UDP；双栈域名应同时具备
A/AAAA 记录及 IPv6 防火墙放行。`CHAT_NATIVE_STUN` 需要至少两个独立可用的 UDP STUN 端点，检测不同目的地址
下的 NAT 映射；不要把同一端点的重复名称当成独立服务器。节点只负责发现和打洞协调，禁用数据转发，不计作 P2P 中继。
原有 WebSocket 中继继续兜底。发布前检查节点监听、STUN 可达性，以及两端的 `path-state` / `path-selected` 诊断。

STUN 节点还必须从客户端所在网络验证：使用同一个 UDP socket 依次访问各节点，并重复请求，比较公网 IP
和端口。不要混用直连出口和代理出口；例如某个域名被解析到 `198.18.0.0/15`，其 STUN 响应可能来自代理的
公网出口，不能拿来推断本机直连 NAT。优先配置至少两个已验证直连的独立节点，再增加一个同样验证过的备用节点。
桌面、无人值守服务和手机共用的原生引擎会过滤此 Fake-IP 段（包括 IPv4-mapped IPv6），覆盖 NAT 检测及
打洞端口映射；不足两个有效探测结果时保持未知，不强行判断为锥形 NAT。普通真实地址的多出口结果仍保守判断，
过滤不替代网络分流配置，也不能保证所有代理或多 WAN 环境都能直连。

仅修正节点配置时，备份现有环境文件并保留当前已验证的 Go 镜像，使用全部原 Compose 文件仅重建 `admin-go`；
无需更换发现节点、TURN 或数据库。新会话会收到新配置；验证日志中的 `udpNatType`、`native-punch` 和
`path-selected`，确认真实选中直连后才判定恢复。浏览器仍使用 WebRTC，其 STUN/TURN 配置和实际媒体路径另行验证。

回滚时清空 `CHAT_NATIVE_PEERS`，重启 admin-go；新会话自动回到原有连接方式。活跃会话会在正常关闭或过期时释放内核。
这项变更不自动部署上述节点，也不代表已获得特定运营商网络下的成功率数据。

## 更新文件副本与动态补源

Go 服务新增公共 GET/HEAD `/update-packages/:version/:name` 与 GET `/update-packages` 状态接口。
GET/HEAD `/update-packages/assets/:id` 可按官方 GitHub 文件 ID 读取同一份已校验缓存，
兼容更新清单中的 GitHub API 下载地址。未知、未缓存或已淘汰的 ID 返回 404，不触发上游请求。
升级后刷新发布列表即可为已有缓存补齐 ID，无需重新下载；首次补齐前，客户端仍可使用官方下载。
Kong 将 `/update-packages` 加入现有 Go 公共路由，保持 `strip_path: false`；不要经过 HTML 回退路由。
此接口只分发预先校验过的官方公开文件，不接受自定义上游地址，不通过请求触发下载。
支持单范围请求、ETag 和 HEAD；最多同时服务 16 个下载请求，繁忙时客户端继续使用其他来源。

Compose 已配置专用持久化卷 `update-packages`，挂载到 `/var/lib/codex-switch/update-packages`，
并设置 `UPDATE_CACHE_DIR`。未设置该变量时不运行缓存任务。缓存目录由单个实例独占；多实例部署时
为各实例分配独立目录/卷。可通过 `UPDATE_CACHE_PROXY` 显式指定 GitHub 出站代理，不更改其他业务代理。

启动后预取、每 15 分钟刷新官方发布列表，按版本号选择最新三个非草稿、非预发布版本。
各版本缓存带 GitHub SHA-256 的 APK、EXE、MSI、ZIP、tar.gz、AppImage、DEB 和 DMG，每个文件最多 512 MiB。
缓存原始发布字节，不修改安装包；先校验再发布。获取文件使用最多 8 MiB 的 HTTP 范围响应，避免每个
512 KiB 分片重复请求。每次获取列表成功后移除保留集合之外的目录；列表失败时保留上一份可用缓存。
更新期间允许临时不足三个完整版本，不保留额外旧版本占用空间。应为三个完整版本及单个暂存文件预留磁盘。
重启恢复先验证摘要，崩溃遗留的下载/索引暂存文件会清理。删除旧目录不会重新创建旧下载源。

新客户端补源请求带 `refill: true`，间隔仍受服务端十秒限制；只补齐三路总上限中的空位。
Redis 事务同时保护接收名额、上传名额和接收端/共享端配对，防止跨实例重复连接。
旧客户端行为兼容；建议先更新全部 Go 实例，再更新桌面与 Android。无需数据库迁移。

验收检查状态接口的版本数不超过三、磁盘不存在更旧版本目录、文件完整 SHA-256、Range/HEAD/ETag、
损坏包不发布，以及共享端中途加入/断开后已完成数据不丢失。客户端安装仍必须通过原有签名校验。

聚合 API 云同步需要先执行 `sql/20261015-aggregate-sync.sql`，再更新 admin-go。
该脚本新增按用户隔离的聚合配置表，保存成员、模型映射、故障切换策略和删除记录；原 Provider 表不变。
更新桌面客户端后使用现有 Provider 同步入口即可同步聚合配置。旧客户端不会覆盖该集合。

端口映射多端同步需要先执行 `sql/20261015-port-mappings.sql`，再更新 admin-go 和客户端。
该脚本仅新增按用户及远程电脑隔离的映射配置表；已有数据库不会自动执行此升级脚本。
新增 GET/PUT `/devices/:deviceId/port-mappings` 使用现有 `/devices` JWT 路由及设备归属校验，
无需新增 Kong 公共路由。上线后验证两端新增、编辑、删除和离线重试；运行状态由各端独立管理。

## SSH 配置多端同步

先执行 `sql/20261024-ssh-profiles.sql`，再更新 admin-go 和客户端。空数据库初始化已包含新表。
配置按登录用户保存，不绑定电脑；保存名称、地址、端口、用户名、登录方式和主机指纹，拒绝密码、私钥和私钥密码。
GET `/sync/ssh-profiles` 读取列表，PUT/DELETE `/sync/ssh-profiles/:id` 按记录版本保存或删除，
POST `/sync/ssh-profiles/import` 仅导入不存在的旧配置。删除记录保留，以阻止旧 PC 再次导入。
现有 `/sync` Kong JWT 路由已覆盖这些接口，保持 `strip_path: false`，无需新增公共路由。
旧 PC 配置在首次使用时上传，成功后移除本地旧副本；导入失败会保留本地数据供重试。
升级后验证同账号跨端新增、修改、删除、账号隔离和冲突提示。回滚时保留表，旧客户端不会读取云端配置。

## 每日下载统计与失败日志

先执行 `sql/20261017-update-download-reports.sql`，再更新 admin-go、admin-ui 及桌面/Android 客户端。
空数据库初始化已包含新表；已有数据库必须显式执行升级脚本。回滚 Go 镜像时保留统计表即可。
将 `/update-sharing/reports` 加入 Kong 的账号 JWT 路由，保持 `strip_path: false`；
`/update-sharing/settings` 继续公开。查询接口 `/admin/api/update-sharing/daily` 和 `/failures`
使用 `/admin/api` 路由和 `admin.update-sharing.read` 权限。

后台「下载设置」按北京时间的服务器接收日期显示最近 7/30/90 天数据，无记录的日期补零。
完整下载且校验成功、从对应来源收到有效数据才计成功次数；服务器用户按登录账号每天去重。
混合下载可同时计为共享成功与服务器成功。流量按本次接受的有效分块统计，包含失败下载已接收的数据，
排除重复分块、本地缓存和此前断点数据，不等于网卡出口流量。官方下载不计入共享/服务器成功量。
统计基于已登录新版客户端上报；旧版、未登录和手动访问公开安装包地址不计入，历史无法补算。
桌面和 Android 上报在后台重试最多三次，同一账号/记录 ID 只保存一次；退出或断网超过重试范围可漏报。
Web 浏览器没有原生安装包共享下载器，沿用现有下载行为，不产生原生下载成功事件。

失败记录保存来源、阶段和受限原因码，并以 `update download failure` 写入服务端结构化日志；
桌面与 Android 客户端的下载统计覆盖共享下载及其官方后备下载，只在最终下载并校验结束时上报一次；
换源成功保留之前的失败原因，但最终结果为成功。旧客户端提前上报的未完成记录无法推断最终结果，
不自动回填或改写；应先更新 Go 缓存服务，再发布新版桌面与 Android 客户端。
后台可按天分页查看，包括换源后最终成功的下载。公开缓存服务失败另记 `update server download failed`
及 HTTP 状态码。不上报原始异常文本、访问令牌、下载地址、连接凭据和本地文件路径。
验收覆盖重复上报、北京时间午夜、多人/同人多次下载、校验失败、混合来源和无权限访问。
