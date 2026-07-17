# 证券交易与经纪系统

## 表面题目

设计证券交易与经纪系统，表面上是展示行情、接受客户买卖和撤单、更新持仓。真正的状态权威分成两层：经纪商可以受理客户意图并预留购买力或持仓，只有交易所或内部撮合簿能宣布实际 fill。成功受理不是成交，撤单请求不是已取消，行情报价也不是未来可得价格；清算与证券交收还发生在成交之后。

本设计聚焦限价与市价订单的经纪入口、前置风控、路由、execution report、撤单和持仓账本。撮合引擎内部算法、投资建议和复杂衍生品估值不是主流程。系统优先避免超成交与风险额度错放，而不是在交易场所失联时假装订单已经终结。

## 反问与边界

先确定资产类别、交易场所、订单类型和有效期，是否允许部分成交、改单、跨场所拆单，客户看到的 accepted、working、partially filled、canceled 和 settled 各代表谁的事实。市价单有价格保护吗，限价依据何种 tick size，盘前盘后与停牌怎样处理。撤单与 fill 同时发生时，合同是否允许“撤单太晚”。

容量模型需要开盘与新闻峰值、热门 symbol 偏斜、每订单平均 fill 数、行情扇出、风控查询和 execution report 积压。SLO 要拆成经纪受理、送达场所、首次回报和客户可见延迟。账户权限、适当性、地区与市场准入不能在过载时跳过；审计需保存客户指令与规则版本。非目标是从本地报价保证一定成交。

## 客观模型

最小命令为 `SubmitOrder(account, symbol, side, qty, limit, key)`、`Cancel(order, client_version)`、`Replace` 和 `IngestExecutionReport(venue_seq)`。经纪订单 owner 保存风险检查、预留、路由 attempt 与单调状态版本；交易场所拥有 order acknowledgment、fill 和 cancel acknowledgment；持仓账本拥有成交后的不可变分录；清算机构拥有交收最终性。

买单预留可近似为 `reserve=quantity×limit_price+estimated_fees`；若被拆成 `n` 个 fill，必须满足 `Σ fill_qty_i + open_qty + canceled_qty = original_qty`。行情读取量可远大于订单写入，但不能因此让行情缓存拥有执行写权限。热点来自单一 symbol 和开盘窗口，账户风控则可能成为另一维热点。

订单状态允许 PENDING_RISK、ACCEPTED、ROUTED、PARTIALLY_FILLED、FILLED、CANCEL_PENDING、CANCELED 与 REJECTED；SETTLED 属于后续层。每个 venue report 以场所序号和 execution ID 去重，累计成交不超过原数量，风险释放只对应权威 canceled/open 减少。

## 必然约束

[DEDUCED:trading-brokerage-order-acceptance-is-not-execution] 经纪商的 ACK 只能证明身份、规则和风险检查已通过并接受处理，不能证明订单已经到达撮合队列。最小反例是 09:30:00 本地 accepted，09:30:00.005 到场所的链路断开，价格随后远离限价；若 ACK 被写成 filled，客户会得到不存在的持仓。成交必须来自带 execution ID 的权威 fill。只有经纪商本身就是唯一撮合 owner 且 ACK 在撮合提交后发出，阶段才可能合并。

[DEDUCED:trading-brokerage-cancel-and-fill-require-a-single-order-sequence] 客户发出 cancel 与订单在场所成交可以并发。100 股订单在 10:00:00 发 cancel，场所于 10:00:00.010 已 fill 100 股，10:00:00.020 本地先写 canceled 并释放购买力；迟到 fill 会同时形成持仓和已释放额度。必须按场所订单事件序列处理 cancel acknowledgment 与 fill，并让风险释放等于确知未成交数量，而不是按请求时间猜测。

[DEDUCED:trading-brokerage-market-data-cannot-authorize-a-guaranteed-fill] 行情是撮合簿过去状态的观察值，在客户端读到最优卖价到订单抵达期间，其他参与者可以先成交或撤单。即使网络只延迟十毫秒，展示的一百股也可能消失。行情可用于风险估值与价格保护，不能作为 guaranteed fill 的授权；若产品承诺保证价格，做市或平台必须显式承担库存风险。

## 从简单方案演进

最简单正确基线是单场所路由：本地持久化 client order、同步前置风控并预留额度，发送稳定 venue client ID，按 execution report 更新订单与账本。低负载时一个按 order_id 串行的消费者即可。用数据库状态最后写胜出会让 canceled 覆盖 fill，按 HTTP 结果推断成交则越过场所权威。

第一个待校准指标是某 symbol 的入口队列 `p99` 超过 20 毫秒，或风控消耗端到端受理预算的 50%。达到后对该 symbol 启用有界准入、每账户速率限制并将 order owner 固定分区；20 毫秒与 50% 是低延迟业务的压测起点，调低会拒绝可承载流量，调高会让订单带着陈旧风险和价格到场。

第二个待校准指标是 execution report 消费滞后超过两秒，或行情 sequence gap 持续五百毫秒。此时暂停受影响场所的新市价单，允许有保护的限价单或明确拒绝，并优先追平回报；两秒与五百毫秒需按资产波动校准。新增代价是可用性降低，但继续用旧行情承诺价格会扩大客户损失。

没有选择把未风控订单先异步送场以降低延迟，因为成交后再拒绝无法撤销市场事实。只有交易场所提供硬信用限额且承担前置拒绝时，部分风控才可下沉。

## 设计决定

本设计以 client_order_id 为经纪状态 owner，先持久化意图并在账户版本上预留风险，再用稳定 venue client ID 路由。ACK、fill、cancel reject 与 cancel acknowledgment 按 venue sequence 进入同一订单状态机；重复 report 只命中原 execution。部分成交立刻提交持仓与现金的平衡账本分录，并按剩余量调整预留。

Cancel 只把订单置为 CANCEL_PENDING，不释放全部额度；收到 fill 就减少 open，收到权威 canceled 才释放剩余。场所超时时订单保持 WORKING_UNKNOWN，查询原 venue ID，不能新建同量订单。清算交收以成交为输入另建状态，失败产生可审计的 fail 与费用，不回写“未成交”。

反选方案是智能路由同时向多个场所发送全量订单、首个成交后撤其他副本，它降低寻价时间却可能并发超成交。只有使用互斥子数量、场所支持可证明的原子取消替换，或经纪商愿意承担多成交库存时才适用。

## 运行与演进

关键 SLI 包括受理延迟、路由 ACK 延迟、execution report lag、累计超成交数、风险预留差异、cancel-too-late 比例、行情 gap、成交到账本延迟和未交收年龄。过载时先减行情个性化与非关键推送，再限制复杂订单和低优先级账户；execution report、风险释放和持仓入账优先于新订单。

故障演练：10:00:00 订单 O1 发往场所并返回 accepted；10:00:01 客户发 cancel；10:00:01.010 场所 fill 60 股，10:00:01.020 cancel 剩余 40 股，但两条回报在网络中倒序到达。本地按 venue sequence 先应用 fill 再 canceled，得到 `60+0+40=100`，只释放 40 股额度。若重启后客户重复 cancel，返回既有终态而不改成交。

状态机和 venue adapter 先录制回报重放做影子对比，再灰度单一 symbol；回滚必须解析新 report 版本。分区迁移用 owner epoch，日志保存规则与路由决定并脱敏账户信息。行情与订单权限隔离，客户端令牌不能伪造 execution report；灾备切换先补齐场所序列再恢复下单。

## 面试考察本质

这题考察的是：给定“客户订单不超成交、风险额度与权威 fill 一致”的不变量，因为经纪商无法从受理 ACK、撤单请求或陈旧行情知道撮合簿最终结果，候选人应推导出订单事件序列、风险预留和成交账本，并在低延迟受理、市场最终性与故障停单之间取舍。

优秀回答会明确交易所是 fill owner，区分 accepted、executed 和 settled，用 cancel/fill 时间线守住数量方程，并说明行情只能观察。常见误区是本地 ACK 即成交、撤单请求即释放额度、最后写胜出合并状态，或为降低延迟绕过硬风控。

二十分钟应讲清订单状态、风控和 execution report；四十分钟加入部分成交、撤单竞态、行情 gap 与账本；六十分钟再讨论多场所路由、清算、灾备和审计。追问可把两条回报倒序送达，要求候选人算出 open、filled、canceled 与 reserve。
