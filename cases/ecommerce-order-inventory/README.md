# 电商订单与库存

## 表面题目

设计电商订单与库存，表面上是加入购物车、结账付款、仓库发货。真正的稀缺对象是某个 SKU 在某个仓库可拣配的实物；订单意图、支付授权和页面显示都不能授予这件实物，只有库存 owner 的 ACTIVE reservation 或由它原子转成的 COMMITTED allocation 才能。成功购买还不是履约完成，捕获、分配、拣货、发货和签收各有独立事实。

本设计聚焦有限库存商品、结账 reservation、支付异步回调、取消和缺货补偿。目录搜索、推荐与退货质检不是关键路径。跨服务不用长事务冻结所有资源，而以明确状态和补偿收敛；不过 saga 不能替代 SKU-仓局部防超卖的原子仲裁。

## 反问与边界

先问库存是精确件数、批次还是可替代容量，安全库存和损耗怎样计算，多仓能否拆单，购物车是否占库存，结账 hold 多久，预售或允许欠货吗。支付在 reservation 前授权还是之后捕获，订单在何时对用户承诺，缺货可换品、延迟或必须退款。取消、退货和仓内损坏分别由谁恢复 on_hand。

规模要看 SKU 数、仓数、热门单品峰值、每订单行数、结账到达率、reservation 存活、支付尾延迟和仓配事件扇出。地域路由应靠近仓库存 owner 而非用户缓存。租户和商家不能写他人库存，价格和促销由服务端固定版本。非目标是从支付成功推断仓库一定存在实物。

## 客观模型

最小命令为 `Reserve(order_line, sku, warehouse, qty, ttl, key)`、`CommitReservation(version)`、`ReleaseReservation(reason)`、`ReleaseAllocation(reason)`、`Ship(allocation_version)` 和 `AcceptReturn`. 库存服务拥有 `on_hand/active_reserved/committed_allocated/safety_stock/allocation_version`，订单服务拥有编排与 capture outbox，支付方拥有授权与 capture，仓库系统拥有拣配、发货和退货验收事实。库存权利只有以下可达路径：`ACTIVE -> COMMITTED_ALLOCATION -> SHIPPED -> RETURNED`，`ACTIVE -> EXPIRED|RELEASED`，以及已证明 capture 没有资金效果时的 `COMMITTED_ALLOCATION -> RELEASED`；ACTIVE 才会到期，COMMITTED_ALLOCATION 不再由 TTL 回收。capture dispatch 状态为 NOT_SENT、SENDING/UNKNOWN、SUCCEEDED 或 FAILED：worker 必须在外部调用前以条件 CAS 从 NOT_SENT 取得 SENDING/UNKNOWN 发送权，一旦取得，PENDING/UNKNOWN 都不能走 RELEASED。

对 SKU `s` 和仓 `w`，`A_s,w=on_hand_s,w-active_reserved_s,w-committed_allocated_s,w-safety_stock_s,w`，Reserve 必须保证 A 不为负。`ACTIVE -> COMMITTED_ALLOCATION` 在一个库存事务中令 active_reserved 减少、committed_allocated 等量增加，A 不变；Ship 同事务令 committed_allocated 与 on_hand 等量减少，A 仍不变。只有实物退回并通过仓库验收后才增加 on_hand；若退货仍在隔离或报损，不得恢复可售量。若结账到达率为 `λ`、平均 reservation 存活为 `h`，仅结账中占用约 `R=λ×h`；支付尾延迟或机器人会把 R 放大。热点由秒杀 SKU 集中，不可用全仓平均库存冲淡。

不变量是一个实物单位只绑定一个 ACTIVE reservation 或 COMMITTED allocation，发货数量不超过 committed allocation，释放必须匹配 reservation/allocation 版本，`active_reserved + committed_allocated` 与权利状态逐项对应，on_hand 的每次变化都有现实收发货或盘点原因。SKU-仓条件扣减与状态转换是库存授权点；capture 发送权与取消必须在同一 outbox 条件状态交汇：发送 CAS 先赢就保留 allocation，取消 CAS 只有在 dispatch 仍为 NOT_SENT 时才可释放，且取消 epoch、allocation version 与 worker lease 会阻止后续发送。订单行、支付和搜索缓存都没有可售量写权限。

## 必然约束

[DEDUCED:ecommerce-order-inventory-payment-authorization-does-not-own-a-physical-unit] 支付授权证明资金路径可能可用，却不改变 SKU-仓的 active_reserved 或 committed_allocated。最小反例是仅剩一件，O1 已授权但未预留，O2 随后成功 Reserve 并付款；若 O1 也因“已授权”进入待发货，同一件商品被承诺两次。只有 ACTIVE reservation 或 COMMITTED allocation 能授予实物；授权成功本身不能创造库存，capture 也不得在仍会到期的 ACTIVE 状态下发出。

[DEDUCED:ecommerce-order-inventory-reservation-must-bind-order-line-and-expiry] reservation 若只有 SKU 和数量，没有唯一 order_line 与版本，取消重试可能释放另一轮已续期占用。时间线是 O1 版本 7 于 10:05 到期并续成版本 8，旧的 Release(7) 在 10:06 到达；无版本检查会把有效版本 8 的库存释放给 O2，随后 O1 仍可能发货。每次提交和释放必须引用确切版本，到期是 owner 的条件事件；准备 capture 时，owner 必须把 ACTIVE v8 原子转为不再过期的 COMMITTED allocation，再允许支付 outbox 对 NOT_SENT 做发送 CAS。发送 CAS 先赢后，即使用户随即取消也必须保留 allocation；只有取消事务在 NOT_SENT 时先写入 cancel epoch 并释放确切 allocation，worker 的 lease generation、取消状态和 allocation version 条件才共同证明其 CAS 必败、外部调用不会发生。

[DEDUCED:ecommerce-order-inventory-oversell-control-is-local-to-sku-warehouse-not-order-row] 两个订单行各自保存“下单时余量一”无法看到对方。O1 与 O2 同时读一并各插入成功，就得到负可售量。防超卖必须交汇于 SKU-仓 owner 的条件扣减、序列或预切库存 token；订单 saga 只能处理跨服务后续，不能修复已经对两个用户承诺的同一实物。

## 从简单方案演进

最简单正确基线是按 SKU-仓执行条件 `available >= qty`，原子增加 active_reserved 并写绑定订单行的 reservation；订单先预留并授权，再把 ACTIVE reservation 原子转成 COMMITTED allocation，最后才让 capture worker 发送。worker 在外部调用前须以 `(dispatch=NOT_SENT, order=PAYMENT_READY, allocation_version, lease_generation)` 做 CAS，持久进入 SENDING/UNKNOWN 后才拥有发送权；从此 allocation 保留、不再到期，编排沿原 reference 查询。取消也用条件事务竞争同一 NOT_SENT：若它先赢，则写 cancel epoch、按版本释放 allocation 并保持 outbox NOT_SENT，后续 worker 因取消状态或 allocation version 不符而不能发送；若 worker 先赢，取消只能进入 CANCEL_REQUESTED，不能释放。除此之外，只有处理器终态 FAILED 等权威证据证明没有资金效果，才可按版本释放。低并发时数据库条件更新足够。购物车只显示近似余量，不默认占库存；否则长期购物车会冻结可售量。

第一个待校准指标是热门 SKU 的 reservation 条件失败率连续五分钟超过 10%，或库存写 `p99` 超过 100 毫秒。达到后为该 SKU 启用库存 token 或有界排队、每用户限购，并把 token 严格切成不重叠份额；10% 与 100 毫秒需按促销压测，调低会增加排队摩擦，调高会让客户端重试压垮 owner。

第二个待校准指标是 `payment_captured && no_committed_allocation` 超过每小时订单的 0.05%，或 SENDING/UNKNOWN capture 的 allocation 保留年龄 `p95` 超过五分钟。此时暂停该类订单的新 capture，扩大原 reference 查询与对账，并对取消后确认成功的 capture 自动退款或人工处理；不能为了降低保留年龄就释放已取得发送权的 UNKNOWN allocation。0.05% 与五分钟按退款损失和库存占用校准。新增成本是转化下降和更多 pending。

没有选择跨订单、支付、库存与仓库的两阶段长事务，因为外部支付和现实拣货不能持有数据库锁。也没有选择付款后才扣库存；只有商品无限可复制或商家明确允许欠货时，该顺序才更优。

## 设计决定

本设计让库存服务按 SKU-仓拥有可售量，订单行先取得带 expires_at、version 和 allocation price 的 ACTIVE reservation。订单编排随后发起支付授权；在有效期内 `CommitReservation` 以一个库存事务将 active_reserved 转成 committed_allocated、生成不再过期的 allocation，然后创建 NOT_SENT capture outbox。worker 只有以订单状态、allocation version 和 lease generation 为 fence 的 CAS 成功写成 SENDING/UNKNOWN，才可用稳定 reference 调用处理器；CAS 失败不得调用。响应丢失以订单行幂等键查询原权利，不新占一份。多行订单允许逐行预留后在短窗口内全体提交，失败释放已有行并明确部分订单政策。

capture SENDING/PENDING/UNKNOWN 时，订单保持 `ALLOCATED_PAYMENT_PENDING`，库存保持 COMMITTED allocation 并沿原 reference 查询；发送权未取得且取消 CAS 在 NOT_SENT 先赢，或处理器返回终态 FAILED，才进入 `PAYMENT_FAILED_COMPENSATING` 并按版本释放，成功则进入 `PAID_FULFILLING`。仓库只拣 committed allocation，Ship 在同一库存事务同时减少 committed_allocated 与 on_hand。取消与退货是有原因的新事件：未发货取消若看到 SENDING/UNKNOWN，只记录 CANCEL_REQUESTED 并保留 allocation，待查询确认失败或成功后退款；若它在 NOT_SENT 先赢，worker fence 保证永不发送，才可释放。已发货只能退款并等待现实退货，验收入库后才增加 on_hand，隔离品不增加可售。账本分别记录支付、退款和商家应付。

反选方案是每仓缓存余量并异步汇总，它读取快且能地域自治，却会共享同一实物份额。只有中央 owner 预先下发不可重叠 token，且仓之间不能超借时，缓存才可成为有限写权威。

## 运行与演进

关键 SLI 包括负 available、重复 allocation、reservation 冲突与年龄、支付有款无货、库存事件 lag、拣货短缺、补偿金额和退款年龄。过载时先降低余量刷新和推荐，再限制购物车查询与单用户 reservation，最后对热品排队；库存提交、支付回调与释放优先于营销流量。

故障演练使用状态机允许的部分失败：10:00 O1 的 ACTIVE v7 原子转成 allocation a7，capture outbox 为 NOT_SENT；10:01:00 worker 以 lease generation 12、未取消状态和 a7 版本做 CAS，先把 dispatch 持久写成 SENDING/UNKNOWN 并取得发送权；10:01:00.010 它用稳定 reference P7 调用处理器，外部成功但响应丢失；10:01:01 用户取消，取消 CAS 看到 SENDING/UNKNOWN，只能写 CANCEL_REQUESTED，a7 继续保持 COMMITTED，绝不能转给 O2；10:01:05 查询 P7 确认成功后进入退款路径。再把竞态顺序反转：若 10:01:00 取消事务在 dispatch 仍为 NOT_SENT 时先写 cancel epoch、释放 a7 且保持 outbox NOT_SENT，则晚到 worker 的订单状态、a7 version 和 lease fence CAS 必须失败，P7 根本不会发送，O2 才能安全取得最后一件。演练分别证明“发送权先赢就保留 allocation”和“取消先赢且确定未发才释放”，不依赖一个违反 fence 的陈旧 worker 制造晚 capture。

库存模型升级先以仓库盘点和事件日志影子计算，灰度非热 SKU；分区迁移用 allocation epoch 避免双 owner。协议回滚继续识别新 reservation 版本。按商家隔离配额和审计调整，顾客地址只进入履约域。促销前用峰值回放验证 token 守恒与补偿吞吐。

## 面试考察本质

这题考察的是：给定“每个 SKU-仓实物只被一个 ACTIVE reservation 或 COMMITTED allocation 占用，且 capture 发送权一旦取得就不得靠取消释放”的不变量，因为订单服务无法从支付授权、缓存余量或无响应知道库存权利与资金事实，候选人应推导出局部条件预留、capture 前原子提交、NOT_SENT 上的发送/取消 CAS、UNKNOWN 时保留 allocation 和跨服务补偿，并在防超卖、热门商品转化与库存占用间取舍。

优秀回答会先写扣除 ACTIVE reserved 与 COMMITTED allocated 的可售量公式，明确库存 owner，区分购物意图、reservation、支付 dispatch、allocation、shipment 和现实退货，用外部成功响应丢失与取消 CAS 的两种先后说明为什么一支保留 allocation、另一支能确定未发后释放。常见误区是 worker 先调用再标记发送、取消把 NOT_SENT 当作无条件事实、ACTIVE 时就 capture、UNKNOWN 时释放、用 saga 替代局部仲裁、无版本释放，或把购物车无限期当库存锁。

二十分钟应讲清 SKU-仓条件更新与订单状态；四十分钟加入多行订单、支付回调、补偿和热品排队；六十分钟再讨论多仓 token、盘点、退货与分区迁移。追问可锁定旧 release 晚到，要求候选人指出版本与写权限。
