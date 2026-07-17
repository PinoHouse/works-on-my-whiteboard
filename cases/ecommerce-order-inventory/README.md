# 电商订单与库存

## 表面题目

设计电商订单与库存，表面上是加入购物车、结账付款、仓库发货。真正的稀缺对象是某个 SKU 在某个仓库可拣配的实物；订单意图、支付授权和页面显示都不能授予这件实物，只有库存 owner 的有效 reservation 才能。成功购买还不是履约完成，捕获、分配、拣货、发货和签收各有独立事实。

本设计聚焦有限库存商品、结账 reservation、支付异步回调、取消和缺货补偿。目录搜索、推荐与退货质检不是关键路径。跨服务不用长事务冻结所有资源，而以明确状态和补偿收敛；不过 saga 不能替代 SKU-仓局部防超卖的原子仲裁。

## 反问与边界

先问库存是精确件数、批次还是可替代容量，安全库存和损耗怎样计算，多仓能否拆单，购物车是否占库存，结账 hold 多久，预售或允许欠货吗。支付在 reservation 前授权还是之后捕获，订单在何时对用户承诺，缺货可换品、延迟或必须退款。取消、退货和仓内损坏分别由谁恢复 on_hand。

规模要看 SKU 数、仓数、热门单品峰值、每订单行数、结账到达率、reservation 存活、支付尾延迟和仓配事件扇出。地域路由应靠近仓库存 owner 而非用户缓存。租户和商家不能写他人库存，价格和促销由服务端固定版本。非目标是从支付成功推断仓库一定存在实物。

## 客观模型

最小命令为 `Reserve(order_line, sku, warehouse, qty, ttl, key)`、`CommitReservation(version)`、`ReleaseReservation(reason)`、`Allocate`、`Ship` 和 `AdjustOnHand`. 库存服务拥有 `on_hand/reserved/safety_stock/allocation_version`，订单服务拥有编排，支付方拥有授权与 capture，仓库系统拥有拣配和发货事实。reservation 状态从 ACTIVE 到 COMMITTED、EXPIRED 或 RELEASED，已发货只能通过退货入库反向变化。

对 SKU `s` 和仓 `w`，`A_s,w=on_hand_s,w-reserved_s,w-safety_stock_s,w`，Reserve 必须保证 A 不为负。若结账到达率为 `λ`、平均 reservation 存活为 `h`，仅结账中占用约 `R=λ×h`；支付尾延迟或机器人会把 R 放大。热点由秒杀 SKU 集中，不可用全仓平均库存冲淡。

不变量是一个实物单位只绑定一个仍有效的订单行，发货数量不超过 committed allocation，释放必须匹配 reservation 版本，库存调整有原因和审计。SKU-仓条件扣减是授权点；订单行、支付和搜索缓存都没有可售量写权限。

## 必然约束

[DEDUCED:ecommerce-order-inventory-payment-authorization-does-not-own-a-physical-unit] 支付授权证明资金路径可能可用，却不改变 SKU-仓的 reserved。最小反例是仅剩一件，O1 已授权但未预留，O2 随后成功 Reserve 并付款；若 O1 也因“已授权”进入待发货，同一件商品被承诺两次。只有仍有效的 reservation 能授予实物，授权失败可释放它，授权成功本身不能创造库存。

[DEDUCED:ecommerce-order-inventory-reservation-must-bind-order-line-and-expiry] reservation 若只有 SKU 和数量，没有唯一 order_line 与版本，取消重试可能释放另一轮已续期占用。时间线是 O1 版本 7 于 10:05 到期并续成版本 8，旧的 Release(7) 在 10:06 到达；无版本检查会把有效版本 8 的库存释放给 O2，随后 O1 仍可能发货。每次提交和释放必须引用确切版本，到期是 owner 的条件事件。

[DEDUCED:ecommerce-order-inventory-oversell-control-is-local-to-sku-warehouse-not-order-row] 两个订单行各自保存“下单时余量一”无法看到对方。O1 与 O2 同时读一并各插入成功，就得到负可售量。防超卖必须交汇于 SKU-仓 owner 的条件扣减、序列或预切库存 token；订单 saga 只能处理跨服务后续，不能修复已经对两个用户承诺的同一实物。

## 从简单方案演进

最简单正确基线是按 SKU-仓执行条件 `available >= qty`，原子增加 reserved 并写绑定订单行的 reservation；订单先预留，再授权和 capture，失败按版本释放。低并发时数据库条件更新足够。购物车只显示近似余量，不默认占库存；否则长期购物车会冻结可售量。

第一个待校准指标是热门 SKU 的 reservation 条件失败率连续五分钟超过 10%，或库存写 `p99` 超过 100 毫秒。达到后为该 SKU 启用库存 token 或有界排队、每用户限购，并把 token 严格切成不重叠份额；10% 与 100 毫秒需按促销压测，调低会增加排队摩擦，调高会让客户端重试压垮 owner。

第二个待校准指标是 `payment_captured && no_valid_reservation` 超过每小时订单的 0.05%，或补偿 `p95` 年龄超过五分钟。此时暂停该类订单的后续 capture，缩短或重新安排结账窗口，并自动退款或人工缺货处理；0.05% 与五分钟按退款损失校准。新增成本是转化下降和更多 pending。

没有选择跨订单、支付、库存与仓库的两阶段长事务，因为外部支付和现实拣货不能持有数据库锁。也没有选择付款后才扣库存；只有商品无限可复制或商家明确允许欠货时，该顺序才更优。

## 设计决定

本设计让库存服务按 SKU-仓拥有可售量，订单行先取得带 expires_at、version 和 allocation price 的 reservation。订单编排随后发起支付授权；在有效期内 CommitReservation 将其转为 allocation，再 capture。响应丢失以订单行幂等键查询原 reservation，不新占一份。多行订单允许逐行预留后在短窗口内全体提交，失败释放已有行并明确部分订单政策。

支付 capture 回调到达时再次验证 reservation version；若已过期且卖给别人，不覆盖新 owner，而进入退款或替代商品补偿。仓库只拣 committed allocation，发货事件引用 allocation。取消与退货是有原因的新事件，延迟 release 按版本无害。账本分别记录支付、退款和商家应付。

反选方案是每仓缓存余量并异步汇总，它读取快且能地域自治，却会共享同一实物份额。只有中央 owner 预先下发不可重叠 token，且仓之间不能超借时，缓存才可成为有限写权威。

## 运行与演进

关键 SLI 包括负 available、重复 allocation、reservation 冲突与年龄、支付有款无货、库存事件 lag、拣货短缺、补偿金额和退款年龄。过载时先降低余量刷新和推荐，再限制购物车查询与单用户 reservation，最后对热品排队；库存提交、支付回调与释放优先于营销流量。

故障演练：10:00 O1 取得最后一件的 reservation v7 并授权；10:05 owner 提交到期释放，O2 取得 v8；10:06 O1 的 capture 成功回调到达。订单服务必须检查 v7 已失效，保留 O2，给 O1 进入退款或替代流程；重复回调返回同一补偿。若旧 Release(v7) 再到达，也不能释放 v8。

库存模型升级先以仓库盘点和事件日志影子计算，灰度非热 SKU；分区迁移用 allocation epoch 避免双 owner。协议回滚继续识别新 reservation 版本。按商家隔离配额和审计调整，顾客地址只进入履约域。促销前用峰值回放验证 token 守恒与补偿吞吐。

## 面试考察本质

这题考察的是：给定“每个 SKU-仓实物只被有效订单行分配一次”的不变量，因为订单服务无法从支付授权或缓存余量知道 reservation 是否仍有效，候选人应推导出局部条件预留、带版本到期和跨服务补偿，并在防超卖、热门商品转化与库存占用间取舍。

优秀回答会先写可售量公式，明确库存 owner，区分购物意图、reservation、支付、allocation 和 shipment，用晚 capture 时间线说明补偿。常见误区是支付成功即有货、用 saga 替代局部仲裁、无版本释放，或把购物车无限期当库存锁。

二十分钟应讲清 SKU-仓条件更新与订单状态；四十分钟加入多行订单、支付回调、补偿和热品排队；六十分钟再讨论多仓 token、盘点、退货与分区迁移。追问可锁定旧 release 晚到，要求候选人指出版本与写权限。
