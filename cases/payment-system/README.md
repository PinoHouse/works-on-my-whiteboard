# 支付系统

## 表面题目

设计支付系统，表面上是商户提交金额，用户验证后得到成功或失败。真正要守住的是同一逻辑 capture 不会因客户端、服务或渠道重试变成多笔可见扣款，并且每个“成功”明确指授权、捕获受理还是资金结算。一个支付意图可以按合同产生多次部分 capture，甚至两次金额相同；它们必须有不同 `capture_id`，而不是被金额误判为重复。支付编排只能拥有本地意图；卡网、钱包或收单处理器拥有外部处理事实，数据库事务不能包住网络另一端。

本设计覆盖创建意图、授权、捕获、退款、回调和对账，不处理具体卡号保管实现。订单购买意图、风控授权、资金捕获、商户账本和外部结算分别建模。目标是对未知结果诚实并最终收敛，不承诺跨机构的瞬时恰好一次。

## 反问与边界

先问支付方式与地区、授权和捕获是否分离、部分 capture 和多次退款是否允许、`capture_id` 与商户幂等键的作用域和保留期、用户何时看到成功、订单能否在 PAYMENT_PENDING 发货。还要确定 3DS 或风险挑战、同一逻辑 capture 最多允许几个渠道 attempt、渠道重试规则、退款与拒付、汇率和手续费由谁固定。成功 SLO 应拆成创建意图、用户交互、渠道响应和最终对账时间。

规模要看每秒意图、每意图尝试数、渠道扇出、回调重复和乱序率、未知结果比例、日终对账量与金额偏斜，而非只看支付行数。安全边界要求最小化敏感数据、签名验证回调、租户隔离幂等键和金额。合规路由不能因为渠道变慢就越过地区或商户限制。非目标是把 HTTP 200 等同商户资金已经可用。

## 客观模型

最小接口为 `CreateIntent(merchant, key, amount)`、`Authorize(intent, method)`、`Capture(intent, capture_id, idempotency_key, amount)`、`Refund(capture_id, amount)`、`IngestCallback` 与 `Reconcile(reference)`。编排服务拥有 immutable intent parameters、逻辑 capture 及其单调 attempt 状态；处理器拥有 authorization/capture 结果；本地复式账本拥有商户应收、渠道清算与费用分录；订单只消费明确语义的事件。一个逻辑 capture 由 `capture_id` 唯一标识，可因明确未受理、受合同保护的重放或路由切换产生多个外部 `attempt_id/reference`，但它们都回写同一 capture。

intent 状态可表示为 CREATED、REQUIRES_ACTION、AUTHORIZED、PARTIALLY_CAPTURED、CAPTURED、SETTLEMENT_PENDING、SETTLED、FAILED 与 REFUNDED；每个逻辑 capture 独立经过 PENDING、UNKNOWN、SUCCEEDED 或 FAILED，未知不是失败。约束包括 CreateIntent 的商户幂等键只映射一个金额与意图，Capture 的 `merchant + idempotency_key` 只映射一个参数完全相同的 `capture_id`，同一 `capture_id` 也不能改金额；不同 `capture_id` 即使金额相同仍是合法的两次部分 capture，前提是累计成功 capture 不超过授权上限。同一 capture 的 attempt 不并发发送，外部最多产生一次有效扣款；UNKNOWN attempt 会封住后续 attempt，除非渠道按 capture 标识去重或已有权威证据证明前次未受理。累计退款不超过各 capture 已捕获金额，外部 reference 与 attempt 唯一绑定并反向归属一个 capture。每次状态变更带版本，旧回调不能覆盖新终态。

若单次外部处理成功概率为 `p`，响应丢失造成未知结果概率为 `u`，未知后盲目额外尝试 `r` 次，则外部请求期望数为 `E[A]=1+r×u`。无处理器级去重时，重复扣款机会随 `u×r` 增长，本地事务数量再少也不能消除。

## 必然约束

[DEDUCED:payment-system-timeout-cannot-distinguish-unsubmitted-from-processed-capture] 本地超时只说明在期限内没有收到答案，无法区分请求未离开主机、处理器已捕获但响应丢失、或仍在排队。最小时间线是 10:00:00 逻辑 capture C1 通过 attempt A1/reference P7 发出 100 元，10:00:01 处理器成功，响应丢失，10:00:05 本地超时；把重试建成 C2 会伪造另一笔部分 capture，以无关联的新 reference 重发 C1 又可能扣两次，直接标失败则让订单与真实扣款冲突。因此 A1 与 C1 必须进入 UNKNOWN，绝不能退回表示尚无外部歧义的 CAPTURE_PENDING；系统只能沿稳定 P7 查询、等待回调或对账，得到权威结果后再把同一 C1 收敛到 CAPTURED 或 FAILED。只有渠道明确按 C1 或原键去重，或能证明 A1 未受理，才可为同一 C1 安全建立后续 attempt。另一次同为 100 元、但商户明确提交不同 C2 且累计额度充足的部分 capture 则是合法业务，不能按金额去重。

[DEDUCED:payment-system-authorization-capture-and-settlement-are-distinct-promises] 授权表示额度暂时可用，capture 表示扣款指令被接受，结算才表示渠道资金进入商户可用头寸。若授权即发货，后续 capture 拒绝会形成坏账；若 captured 即计为银行已结算，财务会忽略清分延迟与拒付。三者由不同 owner 在不同时间决定，必须分别向订单、用户和账本承诺。即时钱包在单一机构内可合并阶段，但仍要明确其合同。

[DEDUCED:payment-system-reconciliation-is-a-correctness-path-not-a-reporting-job] 在同步答案缺失时，本地没有其他事实能证明钱是否移动。外部 reference、签名回调、状态查询和批次清算是补全证据链的唯一来源。若把对账仅当报表，未知 capture 会永久停在失败或被重复执行；因此对账队列的年龄和覆盖率属于正确性 SLI，而非后台运营指标。

## 从简单方案演进

最简单正确基线是一份按 `merchant + idempotency_key` 唯一的意图表，以及一份按 `intent + capture_id` 唯一的逻辑 capture 表；Capture 的独立幂等键固定 capture_id、金额与币种。每个 capture 下面可记录多个外部 attempt，每个 attempt 使用唯一稳定 reference，先持久化再调用单一渠道。同步成功更新所属 capture，超时只把该 attempt 和 capture 写 UNKNOWN；回调和查询以 version 条件推进。低量时人工查看少量未知即可，但不能把失败响应和无响应混为一类，也不能用“金额相同”合并两个合法 partial capture。

第一个待校准指标是某渠道“超时且状态未知”连续十分钟超过 0.3%，或原 reference 查询 `p95` 超过三十秒。达到后暂停自动 capture 重试，扩大查询与对账 worker，并把用户状态显示为处理中。0.3% 和三十秒是根据重复扣款损失设置的起点；调低会增加查询成本，调高会扩大资金与订单不一致窗口。

第二个待校准指标是渠道授权拒绝率或端到端 `p99` 连续十五分钟超过基线两倍，且备用渠道覆盖同一支付方式、地区和合规条件。此时仅把尚未发出的新意图灰度切换，已发送 capture 固定在原渠道至终态。两倍与十五分钟需按季节性校准；切得过快会制造渠道抖动和跨渠道重复。

没有选择用跨数据库与处理器的长事务，因为外部网络不参与本地提交协议。也没有选择所有错误自动换渠道；只有确认原渠道未接收，或两个渠道共享可验证的全局幂等标识时，换路才不增加重复扣款风险。

## 设计决定

本设计以 payment intent 为业务 owner，intent 幂等键固定金额、币种、商户和订单；每个逻辑 capture 再由 `capture_id` 和 capture 幂等键固定金额与目标授权。外部 attempt 先关联 capture 落本地 outbox 与稳定 reference，再由 worker 串行发送；一旦 A1 已跨过外部发送边界而同步响应缺失，attempt 与所属 capture 就以条件更新从 PENDING 进入 UNKNOWN，不能回退。同步响应、回调、主动查询和批次对账先按稳定 reference 找 attempt，再让 UNKNOWN 的同一 capture 单调收敛到 SUCCEEDED/CAPTURED 或 FAILED。重复同一 capture 的请求返回既有 C1 及其 UNKNOWN，参数变化直接拒绝；不同 C2 即便金额相同也按授权剩余额度独立处理。响应丢失不会创建新逻辑 capture 或并发 attempt；若渠道合同允许后续 attempt，它仍属于原 C1，且任一外部成功后封住其余未发送 attempt。

授权、捕获和结算事件分别驱动订单和账本。订单可依据业务风险在 AUTHORIZED 或 CAPTURED 继续，但界面必须写清；账本以不可变分录记录渠道应收、商户余额与费用，不能直接把处理器字符串覆盖余额。UNKNOWN 时不发“失败”通知，超过产品预算后提供人工路径。退款是引用原 capture 的新资金事件。

反选方案是把渠道调用置于数据库事务内并在超时回滚，本地行确实整洁，却无法回滚已在外部完成的扣款。只有支付处理器与账本同处一个原子数据库、没有外部副作用时，该方案才成立。

## 运行与演进

关键 SLI 包括重复可见扣款数、UNKNOWN 比例与年龄、回调验证失败、同键参数冲突、授权到捕获延迟、捕获到结算延迟、对账差异金额和退款年龄。过载时先降低商户报表与非关键 webhook，再限低优先级查询；状态写入、回调摄取和对账不能被新意图流量饿死。

故障演练：10:00:00 逻辑 capture C1 的 attempt A1/reference P7 已持久化并发出，10:00:01 处理器成功，10:00:02 服务故障使成功响应丢失；恢复扫描看到 A1 的已发送标记，在响应重试前把 A1 与 C1 条件推进为 UNKNOWN。10:00:05 客户端以 C1 和原幂等键重试，系统返回同一 C1 的 UNKNOWN 并沿稳定 P7 查询，绝不能返回 CAPTURE_PENDING、创建同金额 C2 或换 reference 重发；10:00:08 回调与 P7 查询都确认成功，并发更新只能让 C1 一次从 UNKNOWN 收敛为 CAPTURED 并写一组账本分录。若查询先权威确认未受理，则同一 C1 才可转 FAILED 或按渠道合同建立后续 attempt。随后商户以新 `capture_id=C2` 合法捕获剩余的同样 100 元时，应建立独立 capture，而迟到的 C1 timeout 不得影响 C2 或降级 C1。

状态机升级先重放历史事件做影子比较，灰度少量商户；回滚继续解析新状态和 reference。密钥轮换保留旧回调验签窗口，敏感字段令牌化，按商户限制数据访问。新增渠道前用故障注入验证超时、重复回调、乱序和批次差异，而不仅验证成功路径。

## 面试考察本质

这题考察的是：给定“同一逻辑 capture 不重复扣款、合法 partial capture 不被误去重且成功语义诚实”的不变量，因为外部 attempt 发出后响应丢失时本地无法知道处理器是否已经捕获，候选人应推导出 C1=UNKNOWN、稳定 P7 查询与单调收敛，以及 intent、`capture_id/idempotency_key`、一对多外部 attempt 和授权/捕获/结算分层，并依据重复扣款损失与等待体验决定何时查询、降级或切渠道。

优秀回答会把 UNKNOWN 画成一等状态，说明谁拥有外部事实，让 capture 幂等键固定 `capture_id` 与请求参数，区分“重试 C1”和“同金额新建 C2”，并用 A1 外部成功响应丢失后沿 P7 收敛的时间线解释为何 CAPTURE_PENDING 或重发都不是真实恢复。常见误区是宣称消息队列提供端到端恰好一次、把授权当到账、在未知时新建 capture、把 UNKNOWN 降回 PENDING、按金额误杀 partial capture，或把对账当可延期报表。

二十分钟应讲清 intent、attempt、状态机和重复防护；四十分钟加入 3DS、部分捕获、退款、回调与账本；六十分钟再讨论多渠道路由、拒付、合规隔离和密钥轮换。追问应固定在“外部成功、本地无响应”，要求候选人逐项说明订单、用户、账本和重试看到什么。
