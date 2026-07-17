# 发布订阅系统

## 表面题目

设计发布订阅系统，表面是发布者向主题写一次事件，多个订阅者分别收到。真正的状态变化包括事件进入主题保留、每个订阅按某个过滤版本产生逻辑投递，以及该订阅独立推进确认游标。成功必须拆成发布接受、投递尝试、端点确认和用户副作用；前一项不能证明后几项。它与工作队列不同：订阅者不是共同分摊一个游标，而是每个 subscription 都拥有独立恢复边界，慢端点不能删除或阻塞健康端点仍需的事件。

## 反问与边界

先问订阅是推送、拉取还是两者兼有，过滤规则在发布时还是消费时判定，规则更新是否追溯历史；再问发布成功覆盖什么故障域、每订阅的交付 SLO、最大重试期、死信处理和端点幂等能力。容量用发布率 `λ`、事件大小、平均与 `p99` 匹配订阅数 `F`、每订阅积压和尝试次数表达，不能把一次主题写等同于一次工作。需要明确租户配额、私有主题授权、载荷加密、删除传播和 webhook 出站安全。

排序域最多是 topic partition 或另行声明的单订阅顺序；不同订阅的到达时间不可比较。重放边界是事件仍在 retention，且该订阅 cursor 或显式 replay 请求仍指向它。背压按 subscription 隔离在投递队列、租约、端点并发和出站带宽；主题 append 不应被一个故障 webhook 卡住。非目标是承诺所有设备同时看到事件，或从 HTTP 2xx 推断终端用户已处理。

## 客观模型

最小接口为 `Publish(topic,event_id,payload)`、`CreateSubscription(topic,filter,mode)`、`Pull(subscription,cursor,limit)`、`Ack(subscription,event_id,delivery_attempt)` 与 `DeleteSubscription(expected_delivery_generation)`。核心状态分为 `topic -> {partitions,retention}` 和 `subscription -> {ACTIVE|DELETING|DELETED,filter_version,delivery_generation,cursor,delivery_attempts,push_egress_leases,dead_letters}`。主题日志 owner 持有可重放事件，subscription coordinator 持有该订阅的过滤版本、delivery generation、投递记录和恢复游标；实际 push egress gate 拥有跨越外部调用边界的提交点，发布者不拥有任何订阅 ACK。

不变量是：每个订阅按审计得到的过滤版本决定事件是否匹配；同一逻辑投递使用稳定 `(subscription_id,event_id)`；快订阅推进不改变慢订阅状态；旧租约 owner 不能覆盖新尝试；删除进入 DELETING 后提升 delivery generation、停止创建新 delivery 并立即拒绝新的 pull，只有当前 push egress gate 全部 ACK 旧 generation 已 fence 或其可强制短租约到期后才提交 DELETED；DELETED 后旧 generation 不能新跨外部边界，已经跨过边界的数据无法召回；保留到期必须显式终止重放权。发布率 `λ`、平均匹配数 `F`、平均尝试 `a`、事件字节 `b` 时，逻辑投递工作约为 `λ×F×a`，出站字节约为 `λ×F×a×b`。订阅 s 的积压满足 `Q_s(t+Δ)=max(0,Q_s+(λ_match,s-μ_s)×Δ)`。

丢失边界包括 publish ACK 前客户端放弃、retention 到期而订阅未追上，以及先推进 cursor 后调用端点；重复边界是端点已执行但响应丢失后的重投。topic partition 只给源事件局部顺序，按过滤和并发投递后若仍要求订阅内顺序，就必须限制同一 ordering key 的并发和重试越过。

## 必然约束

[DEDUCED:pubsub-subscription-fanout-requires-independent-progress] S1 和 S2 对事件 E 的处理速度不同，系统在任一时刻不可能用一个数字同时表达两者进度。最小反例是 E=offset 50，S2 已 ACK、S1 尚未收到；把全局 cursor 推到 51 会让 S1 永久漏记，停在 50 又会让 S2 被无关故障阻塞。因此每个订阅必须拥有自己的 cursor、attempt 和保留判断。只有多个 worker 明确属于同一竞争消费订阅时，才共享该订阅内部的分配状态。

[DEDUCED:pubsub-publish-acceptance-is-not-all-subscriber-delivery] publish ACK 的线性化点是事件进入主题持久保留，订阅端点可能尚未注册、离线或已经执行但响应丢失。若要在 ACK 前等待所有订阅，就会让动态订阅集合和最慢端点成为发布协调点，甚至无法定义“所有”。结论是发布可用性与每订阅交付 SLO分开报告；端点边界通常至少一次。对同步、固定且很小的接收者集合，可选择协调等待，但那已是另一种事务广播契约。

[DEDUCED:pubsub-filter-version-is-part-of-delivery-semantics] 事件 E 在规则 v3 下匹配、在 v4 下不匹配时，重试 worker 若只读取“最新规则”会让同一次逻辑投递因时间不同而改变。最小反例是首次投递已触发邮件，规则更新后响应丢失，重试用 v4 判为不匹配并直接 ACK，审计将无法解释状态。必须将 filter version 绑定到事件评估或投递记录；若产品明确选择规则更新重算历史，则应创建新的 replay generation，而不是静默重解释。

[DEDUCED:pubsub-subscription-deletion-must-fence-push-egress] 推送 worker 可能在订阅仍为 ACTIVE 时领取 delivery lease，随后暂停；若删除只改 coordinator 状态，worker 恢复后仍能调用旧端点，状态 fencing 最多拒绝它推进 cursor，不能撤回外部字节。删除必须提升 delivery generation、停止创建新 delivery、让 Pull 立即拒绝新读，并让实际 push egress gate 在跨外部边界时校验 generation 与一次性短租约。删除保持 PENDING，直到撤权开始时所有 gate ACK 旧 generation 已 fence，或其可强制短租约失效；已经跨过边界的 push 只能审计和对账，不能召回。

## 从简单方案演进

基线是每个订阅各维护一个队列，发布时同步复制到所有队列。它对少量固定订阅简单，但存储与写入随 `F` 放大，慢端点还可能拖慢发布。当匹配 fanout 的 `p99` 超过设计预算的 80%，或同步分发 CPU 高于节点预算的 70%，演进为共享主题保留加每订阅 cursor，异步创建投递。新增成本是过滤索引、独立积压和 retention 协调，但发布路径不再等待端点。

推送端点不稳定后，为每订阅加入带租约重试、指数退避、死信和实际 push egress gate。worker 的 delivery lease 只授权申请绑定 delivery generation 与 deadline 的一次性短 egress lease；gate 在跨越外部调用边界的同一临界区复核并消费，worker 没有旁路出口。当某订阅最老未 ACK 年龄超过交付 SLO 的 50%，且 backlog 增长速度高于其消费率 20%，暂停主动推送并切为有游标拉取或人工死信审查；其他订阅继续。稳定过滤器可编译为倒排索引降低每事件扫描，高频变化规则仍运行时判定并记录版本，避免错误预路由。

上述 80%、70%、50% 和 20% 是待负载测试、故障演练和产品风险校准的初始参数，不是实测事实。更早隔离会增加订阅状态和冷启动成本，更晚隔离会扩大积压与端点洪峰。反选“每订阅完整复制一份主题”在订阅数极少、强物理隔离且保留成本可接受时重新变优；大规模 fanout 下它把主题存储乘以订阅数。

## 设计决定

本设计先将事件持久追加到主题，再按固定的事件位置、filter version 和 delivery generation 生成各订阅投递。拉取订阅以 cursor 和 credit 控制背压，并在每次 Pull 校验订阅仍为 ACTIVE；推送订阅以 delivery lease、一次性短 egress lease、实际 push egress gate、并发上限、退避和稳定 delivery key 交付。gate 在外发点校验订阅 ACTIVE、delivery generation 与 lease deadline，消费授权并跨越调用边界；失效授权不发。端点超时保持投递未知并重试，不提前 ACK；若端点支持 idempotency key，以 `(subscription,event_id)` 去重，否则契约明确可能重复。订阅 cursor 只在投递完成或被显式送入死信后推进。

排序按 ordering key 在单订阅内串行，其他 key 可以并发；故障 key 不阻塞整个 topic。发布确认不等待分发，健康订阅也不等待慢订阅。过载先降低低优先订阅推送并发、转拉取、暂停历史 replay，最后才限制 publish。删除订阅先以条件事务进入 DELETING、提升 delivery generation、停止创建新 delivery，并让 Pull 从此拒绝新读；协调器冻结当时的 push egress gate/短租约集合，等待各 gate ACK fence 或无法联系者的可强制短租约到期。barrier 未完成时删除返回 PENDING 并暴露剩余上界，完成后才标记 DELETED；本地 fence 前已跨外部边界的 push 无法召回，仍按原 delivery key 记录响应或 UNKNOWN。

未选择“发布者保存所有订阅地址并直接广播”，因为发布者会耦合动态注册、重试和密钥，无法独立演进。固定少量内部服务、要求同步失败反馈时它才更简单。也不承诺端点副作用 exactly-once，除非端点将 delivery key 与业务写原子提交。

## 运行与演进

SLI 包括 publish 持久延迟、每订阅最老 backlog 年龄、匹配 fanout 分布、投递尝试数、端点错误率、死信率、删除 PENDING 年龄、旧 delivery generation 的 egress 拒绝、gate ACK/短租约 barrier 时长、过滤 CPU 和 retention 余量。总体成功率必须按 subscription 与租户分解，避免大量健康订阅掩盖一个关键账单订阅停摆。多租户限制订阅数、过滤复杂度、并发和出站带宽；webhook 目标需要域名授权、凭证轮换与内网访问防护。

交付故障时间线：0 ms，E 写入 retention；10 ms，S1、S2 各建立 delivery；30 ms，S1 的 HTTP 请求已执行但响应丢失，S2 ACK 并推进自己的 cursor；60 ms，S1 lease 到期，接管 worker 以同一 event ID 重试，端点去重或暴露重复；S2 不回退。删除时间线：worker W 持 S1 delivery generation 12 的短 egress lease 在 gate 前暂停；100 ms 删除把 S1 提升到 13、停止新 delivery 且 Pull 立即拒绝，API 返回 PENDING；130 ms gate ACK fence 或旧短租约到期，删除才提交；140 ms W 恢复时在外部调用前被拒绝。反向分支若 W 在 90 ms 已跨边界，删除不能召回该 push，只能保留其 attempt/UNKNOWN。演练验证发布记录仍可重放、两订阅状态独立、删除 barrier 后旧 generation 不再新外发，并明确无法知道终端用户是否看见。

过滤升级先冻结 v3 投递代际，影子评估 v4 的匹配差异，再让新事件使用 v4；需要历史重算时创建可观察 replay generation，回滚只切回新事件规则，不删除已产生投递。retention 缩短前检查最慢关键订阅并导出快照，否则到期就是明确丢失边界。区域灾备需分别声明主题复制与订阅状态复制的恢复点。

## 面试考察本质

给定“每个订阅都能独立恢复其匹配事件，且删除 barrier 完成后旧 delivery generation 不再新跨 push egress”的不变量，因为发布者无法即时知道所有端点是否可用、一个 ACK 不能代表其他订阅进度，删除协调器也不知道暂停 worker 是否已跨外部边界，候选人应推导出共享主题事实、每订阅 cursor、过滤版本、delivery generation、隔离背压和 gate ACK/短租约撤权 barrier。主导取舍是 fanout 成本、交付延迟、重试重复、删除完成延迟、最大外发暴露窗和每订阅状态，不应把它回答成一个 consumer group，也不能把控制面删除冒充即时召回。

优秀信号包括写出 `λ×F×a`、区分竞争消费与广播、说明 filter version 的审计语义、把慢订阅隔离到拉取或死信，并准确定位 publish 接受与端点副作用的边界。常见误区是用全局 offset、让最慢 webhook 阻塞发布、把 HTTP 2xx 当用户已读，或把最新过滤器套到所有历史重试。

二十分钟应完成主题、订阅、cursor 和发布/交付语义；四十分钟加入过滤索引、租约重试、死信和顺序键；六十分钟再讨论动态订阅、规则回放、区域恢复、租户安全与保留迁移。追问可用 S1 响应丢失、S2 正常前进的反例，要求候选人逐一说明排序域、重放边界、背压位置、状态 owner 和重复发生点。
