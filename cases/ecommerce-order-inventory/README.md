# 电商订单与库存

## 表面题目

设计电商订单与库存，表面上是加入购物车、结账付款、仓库发货。真正的稀缺对象是某个 SKU 在某个仓库可拣配的实物；订单意图、支付授权和页面显示都不能授予这件实物，只有库存 owner 的 ACTIVE reservation 或由它原子转成的 COMMITTED allocation 才能。成功购买还不是履约完成，捕获、分配、拣货、发货和签收各有独立事实。

本设计聚焦有限库存商品、结账 reservation、支付异步回调、取消和缺货补偿。目录搜索、推荐与退货质检不是关键路径。跨服务不用长事务冻结所有资源，而以明确状态和补偿收敛；不过 saga 不能替代 SKU-仓局部防超卖的原子仲裁。

## 反问与边界

先问库存是精确件数、批次还是可替代容量，安全库存和损耗怎样计算，多仓能否拆单，购物车是否占库存，结账 hold 多久，预售或允许欠货吗。支付在 reservation 前授权还是之后捕获，订单在何时对用户承诺，缺货可换品、延迟或必须退款。取消、退货和仓内损坏分别由谁恢复 on_hand。

规模要看 SKU 数、仓数、热门单品峰值、每订单行数、结账到达率、reservation 存活、支付尾延迟和仓配事件扇出。地域路由应靠近仓库存 owner 而非用户缓存。租户和商家不能写他人库存，价格和促销由服务端固定版本。非目标是从支付成功推断仓库一定存在实物。

## 客观模型

最小命令为 `Reserve(order_line, sku, warehouse, qty, ttl, key)`、`CommitReservation(commit_request_id, order_line, reservation_version, capture_reference)`、`ReleaseReservation(order_line, reservation_version, reason)`、`ApplyReleaseIntent(intent_id, allocation_version, reason)`、`Ship(allocation_version)` 和 `AcceptReturn`。库存服务拥有 `on_hand/active_reserved/committed_allocated/safety_stock/allocation_version`，并拥有持久的 `capture_handoff/allocation_commit` 与 `allocation_release_ack` 事实；订单服务拥有编排、capture outbox/dispatch、capture fence 和 release intent 的投递进度，支付方拥有授权与 capture，仓库系统拥有拣配、发货和退货验收事实。库存权利只有以下可达路径：`ACTIVE -> COMMITTED_ALLOCATION -> SHIPPED -> RETURNED`，`ACTIVE -> EXPIRED|RELEASED`，以及已证明 capture 没有资金效果时的 `COMMITTED_ALLOCATION -> RELEASED`；ACTIVE 才会到期，COMMITTED_ALLOCATION 不再由 TTL 回收。capture dispatch 状态为 NOT_SENT、SENDING/UNKNOWN、SUCCEEDED 或 FAILED：只有订单/发送权威中的 worker 在外部调用前以条件 CAS 从 NOT_SENT 取得 SENDING/UNKNOWN 发送权，才可调用支付；一旦取得，SENDING/UNKNOWN 都不能发出 release intent。

对 SKU `s` 和仓 `w`，`A_s,w=on_hand_s,w-active_reserved_s,w-committed_allocated_s,w-safety_stock_s,w`，Reserve 必须保证 A 不为负。`ACTIVE -> COMMITTED_ALLOCATION` 在一个库存事务中令 active_reserved 减少、committed_allocated 等量增加，A 不变；Ship 同事务令 committed_allocated 与 on_hand 等量减少，A 仍不变。只有实物退回并通过仓库验收后才增加 on_hand；若退货仍在隔离或报损，不得恢复可售量。若结账到达率为 `λ`、平均 reservation 存活为 `h`，仅结账中占用约 `R=λ×h`；支付尾延迟或机器人会把 R 放大。热点由秒杀 SKU 集中，不可用全仓平均库存冲淡。

不变量是一个实物单位只绑定一个 ACTIVE reservation 或 COMMITTED allocation，发货数量不超过 committed allocation，释放必须匹配 reservation/allocation 版本，`active_reserved + committed_allocated` 与权利状态逐项对应，on_hand 的每次变化都有现实收发货或盘点原因。SKU-仓条件扣减与状态转换是库存授权点；capture 发送权与取消只在订单/发送权威内对同一 dispatch 交汇：发送 CAS 先赢就保留 allocation，取消只有在 dispatch 不存在或仍为 NOT_SENT 时才能先持久化 capture fence，并在已知 allocation_version 后生成版本化 release intent。取消获胜只证明禁止发送，不等于库存已经释放；库存 owner 必须按 allocation_version 在自己的事务中幂等释放并写含 `(intent_id, allocation_version, result, current_state)` 的 ACK，对账器在 ACK 前持续重投。只有 APPLIED 或由库存历史证明目标版本已经 RELEASED 的 ALREADY_RELEASED 才能关闭释放补偿，SHIPPED/CONFLICT 必须转退款、退货或人工处理。订单行、支付和搜索缓存都没有可售量写权限；两个 owner 各自只提交自己的状态，并以持久事实衔接。

## 必然约束

[DEDUCED:ecommerce-order-inventory-payment-authorization-does-not-own-a-physical-unit] 支付授权证明资金路径可能可用，却不改变 SKU-仓的 active_reserved 或 committed_allocated。最小反例是仅剩一件，O1 已授权但未预留，O2 随后成功 Reserve 并付款；若 O1 也因“已授权”进入待发货，同一件商品被承诺两次。只有 ACTIVE reservation 或 COMMITTED allocation 能授予实物；授权成功本身不能创造库存，capture 也不得在仍会到期的 ACTIVE 状态下发出。

[DEDUCED:ecommerce-order-inventory-reservation-must-bind-order-line-and-expiry] reservation 若只有 SKU 和数量，没有唯一 order_line 与版本，取消重试可能释放另一轮已续期占用。时间线是 O1 版本 7 于 10:05 到期并续成版本 8，旧的 Release(7) 在 10:06 到达；无版本检查会把有效版本 8 的库存释放给 O2，随后 O1 仍可能发货。每次提交和释放必须引用确切版本，到期是库存 owner 的条件事件；准备 capture 时，订单先持久化稳定 commit_request_id 与 capture_reference，库存 owner 再在自己的事务中把 ACTIVE v8 转为不再过期的 COMMITTED allocation，并同时持久化含 order_line、allocation_version 与 capture_reference 的 `allocation_commit` 事实。订单 owner 幂等消费该事实后，才在自己的事务中创建或推进 NOT_SENT outbox；两步之间允许崩溃并靠事实重放或按 commit_request_id 查询恢复。发送 CAS 先赢后，即使用户随即取消也必须保留 allocation；取消先赢只在订单侧写 capture fence，并通过版本化 release intent/ACK 请求库存释放，不能把两个 owner 的状态变化描述成一次事务。

[DEDUCED:ecommerce-order-inventory-oversell-control-is-local-to-sku-warehouse-not-order-row] 两个订单行各自保存“下单时余量一”无法看到对方。O1 与 O2 同时读一并各插入成功，就得到负可售量。防超卖必须交汇于 SKU-仓 owner 的条件扣减、序列或预切库存 token；订单 saga 只能处理跨服务后续，不能修复已经对两个用户承诺的同一实物。

## 从简单方案演进

最简单正确基线是按 SKU-仓执行条件 `available >= qty`，原子增加 active_reserved 并写绑定订单行的 reservation；订单先预留并授权，在本地持久化 commit_request_id 与 capture_reference 后请求提交。库存 owner 幂等处理 commit_request_id，在一个库存事务中把 ACTIVE reservation 转成 COMMITTED allocation，并写持久 `allocation_commit(handoff_id, order_line, allocation_version, capture_reference)`；发布器至少一次重放该事实，订单 inbox 幂等消费后在一个订单事务中创建 NOT_SENT capture outbox。worker 在外部调用前须以 `(dispatch=NOT_SENT, capture_fence=false, allocation_version, lease_generation)` 做 CAS，持久进入 SENDING/UNKNOWN 后才拥有发送权；从此 allocation 保留、不再到期，编排沿原 reference 查询。取消在同一订单权威内竞争 NOT_SENT：若它先赢，则持久写 cancel epoch/capture fence，并把 `(intent_id, order_line, allocation_version, reason)` 写入 release-intent outbox；库存按版本幂等释放、持久化处理结果并发 ACK，订单在得到安全终态 ACK 前保持 RELEASE_PENDING。若 worker 先赢，取消只能进入 CANCEL_REQUESTED，不能发 release intent；只有处理器终态 FAILED 等权威证据证明没有资金效果，才可另发版本化 release intent。低并发时两个 owner 各自的数据库条件更新与持久消息重放足够。购物车只显示近似余量，不默认占库存；否则长期购物车会冻结可售量。

第一个待校准指标是热门 SKU 的 reservation 条件失败率连续五分钟超过 10%，或库存写 `p99` 超过 100 毫秒。达到后为该 SKU 启用库存 token 或有界排队、每用户限购，并把 token 严格切成不重叠份额；10% 与 100 毫秒需按促销压测，调低会增加排队摩擦，调高会让客户端重试压垮 owner。

第二个待校准指标是 `payment_captured && no_committed_allocation` 超过每小时订单的 0.05%，或 SENDING/UNKNOWN capture 的 allocation 保留年龄 `p95` 超过五分钟。此时暂停该类订单的新 capture，扩大原 reference 查询与对账，并对取消后确认成功的 capture 自动退款或人工处理；不能为了降低保留年龄就释放已取得发送权的 UNKNOWN allocation。0.05% 与五分钟按退款损失和库存占用校准。新增成本是转化下降和更多 pending。

没有选择跨订单、支付、库存与仓库的两阶段长事务，因为外部支付和现实拣货不能持有数据库锁。也没有选择付款后才扣库存；只有商品无限可复制或商家明确允许欠货时，该顺序才更优。

## 设计决定

本设计让库存服务按 SKU-仓拥有可售量，订单行先取得带 expires_at、version 和 allocation price 的 ACTIVE reservation。订单编排随后发起支付授权，在订单本地事务中固定 commit_request_id 与 capture_reference；在有效期内 `CommitReservation` 以一个库存事务将 active_reserved 转成 committed_allocated、生成不再过期的 allocation，同时追加 `allocation_commit` 事实，事务到此结束。库存发布器和对账器持续投递该事实；订单 inbox 以 handoff_id/allocation_version 去重，在订单本地事务中记录 handoff 并创建或推进对应 capture outbox。即使库存提交后订单崩溃，事实仍可重放，既不会遗失 capture，也不要求把库存行与订单 outbox 放进一个提交点。worker 只有以订单状态、allocation version、capture fence 和 lease generation 为条件的 CAS 成功写成 SENDING/UNKNOWN，才可用稳定 reference 调用处理器；CAS 失败不得调用。响应丢失沿原 reference 查询，不新占一份。多行订单允许逐行预留后在短窗口内全体提交，失败通过各 owner 的版本化释放协议收敛并明确部分订单政策。

capture dispatch 为 SENDING/UNKNOWN 时，订单保持 `ALLOCATED_PAYMENT_PENDING`，库存保持 COMMITTED allocation 并沿原 reference 查询；SUCCEEDED 后，订单 owner 才在本地事务中进入 `PAID_FULFILLING` 并写履约 outbox，仓库只消费该履约事实并拣匹配版本的 committed allocation。发送权未取得且取消在订单事务中对 NOT_SENT 先写 capture fence，或处理器返回终态 FAILED，才进入 `PAYMENT_FAILED_COMPENSATING` 或 `CANCEL_RELEASE_PENDING` 并生成版本化 release intent。库存 inbox 以 intent_id 去重，在匹配 allocation_version 且仍为 COMMITTED 时以库存事务转为 RELEASED、扣减 committed_allocated，并写可重放 ACK；ACK 携带 result 与 current_state，订单只把 APPLIED 或有历史证明的 ALREADY_RELEASED 当作释放闭合。Ship 与 release 在库存 owner 本地按同一 allocation_version/state 条件仲裁：release 已赢时迟到 Ship 必须失败，Ship 已赢时 release 返回 SHIPPED，订单转退款、退货或人工处理而非关闭补偿。

若取消发生时 handoff 尚未到达，订单先按 order_line、reservation_version 和 commit_request_id 持久化 fence，迟到 handoff 只能补出 release intent，不能创建可发送 outbox。对账器同时查询库存权威的 commit_request_id 结果：若为 COMMITTED，则取得或重放原 handoff 后生成 release intent；若仍为 ACTIVE，则以确切 reservation_version 执行 ReleaseReservation；若为 EXPIRED/RELEASED，则记录库存终态证明并关闭；只有库存不可达或结果仍不确定时继续 pending。若未发货取消看到 SENDING/UNKNOWN，只记录 CANCEL_REQUESTED 并保留 allocation，待查询确认失败后再走 release intent，或确认成功后退款；绝不因取消已落库就假定库存已释放。已发货只能退款并等待现实退货，验收入库后才增加 on_hand，隔离品不增加可售。账本分别记录支付、退款和商家应付。

反选方案是每仓缓存余量并异步汇总，它读取快且能地域自治，却会共享同一实物份额。只有中央 owner 预先下发不可重叠 token，且仓之间不能超借时，缓存才可成为有限写权威。

## 运行与演进

关键 SLI 包括负 available、重复 allocation、reservation 冲突与年龄、支付有款无货、库存事件 lag、拣货短缺、补偿金额和退款年龄。过载时先降低余量刷新和推荐，再限制购物车查询与单用户 reservation，最后对热品排队；库存提交、支付回调与释放优先于营销流量。

故障演练一覆盖“库存提交后订单崩溃”：10:00:00 库存事务把 O1 的 ACTIVE v7 转成 allocation a7，并持久写入 handoff H7，随后提交；10:00:00.010 订单进程在收到响应或创建 outbox 前崩溃。10:00:05 库存发布器或对账器重放 H7，订单 inbox 以 H7/a7 去重并在本地事务中创建唯一 NOT_SENT/P7。恢复只依赖已提交事实，不回滚 a7；库存提交与订单 outbox 各有独立恢复点。

故障演练二覆盖发送权先赢：10:01:00 worker 以 lease generation 12、capture_fence=false 和 a7 做 CAS，先把 dispatch 持久写成 SENDING/UNKNOWN；10:01:00.010 它用 P7 调用处理器，外部成功但响应丢失。10:01:01 用户取消只能写 CANCEL_REQUESTED，不能生成 RI7，a7 继续保持 COMMITTED；10:01:05 沿 P7 查询确认成功后进入退款路径。worker 已取得发送权时，订单结果必须保持 UNKNOWN/查询，不能用取消释放库存。

故障演练三是独立的取消先赢分支，覆盖“取消落库后释放丢失”：10:02:00 O2 的 P8/a8 仍为 NOT_SENT，取消事务先持久化 cancel epoch 9、capture fence 和 release intent RI8；worker 随后因 fence 失败，支付不会调用。RI8 首次投递丢失，订单仍为 CANCEL_RELEASE_PENDING；对账器因未见 ACK 重投相同 RI8，库存 inbox 按 intent_id/allocation_version 在本地事务中释放 a8 并写 `A8(result=APPLIED,current_state=RELEASED)`。A8 即使再次丢失也可重放，订单只在收到这个安全终态后关闭取消。

故障演练四覆盖迟到、无 handoff 与重复：O3 的库存已经提交 H9，但响应超时后订单先按 order_line/reservation v9/commit request C9 写取消 fence，迟到 H9 只能被记录并生成 RI9，不得创建可发送的 P9；若 C9 实际未送达或被拒，对账查询会看到 ACTIVE、EXPIRED 或 RELEASED，并分别按 v9 释放 reservation 或凭终态关闭，不会无限等待 H9。若 a9 已释放、订单行后来取得 a10，迟到或重复 RI9 必须查到 a9 的释放历史并返回 `ALREADY_RELEASED(a9)`；若查不到目标版本的安全终态则返回 CONFLICT，绝不能释放 a10 或关闭补偿。重复 H9 由订单 inbox 唯一键收敛为同一个 P9 或 RI9，重复 RI9 由库存 inbox 返回同一处理结果，重复 ACK 也不重复推进状态。演练证明至少一次消息、乱序和任一侧崩溃都由持久事实、owner 本地条件与对账闭合。

库存模型升级先以仓库盘点和事件日志影子计算，灰度非热 SKU；分区迁移用 allocation epoch 避免双 owner。协议回滚继续识别新 reservation 版本。按商家隔离配额和审计调整，顾客地址只进入履约域。促销前用峰值回放验证 token 守恒与补偿吞吐。

## 面试考察本质

这题考察的是：给定“每个 SKU-仓实物只被一个 ACTIVE reservation 或 COMMITTED allocation 占用，且 capture 发送权一旦取得就不得靠取消释放”的不变量，因为订单服务无法从支付授权、缓存余量或无响应知道库存权利与资金事实，候选人应推导出库存 owner 的局部条件预留与提交、同库存事务持久化 allocation_commit handoff、订单 owner 幂等创建 outbox、NOT_SENT 上的发送/取消本地竞争、取消后的版本化 release intent/结果化 ACK、无 handoff 时按 commit request 查询、UNKNOWN 时保留 allocation、capture 成功后才发布履约和持续对账，并在防超卖、热门商品转化与库存占用间取舍。

优秀回答会先写扣除 ACTIVE reserved 与 COMMITTED allocated 的可售量公式，明确库存 owner 与订单/发送 owner，区分购物意图、reservation、allocation_commit handoff、支付 dispatch、release intent/ACK、fulfillment intent、shipment 和现实退货，用库存提交后订单崩溃、取消释放丢失、无 handoff、迟到重复事件以及外部成功响应丢失说明每个 owner 如何恢复。常见误区是让库存事务顺便创建订单 outbox、让取消事务直接释放库存、收到任意 ACK 就关闭、COMMITTED 就直接拣货、worker 先调用再标记发送、ACTIVE 时就 capture、UNKNOWN 时释放、用 saga 替代局部仲裁、无版本释放，或把购物车无限期当库存锁。

二十分钟应讲清 SKU-仓条件更新与订单状态；四十分钟加入多行订单、支付回调、补偿和热品排队；六十分钟再讨论多仓 token、盘点、退货与分区迁移。追问可锁定旧 release 晚到，要求候选人指出版本与写权限。
