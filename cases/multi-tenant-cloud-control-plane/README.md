# 多租户云控制平面

## 表面题目

设计多租户云控制平面，表面上是让客户创建、更新和删除实例、网络或数据库，后台调用多个资源 provider。真正的状态变化是租户资源的版本化 desired spec 被接受为可恢复 operation saga，再逐步与 provider 的异步 observed facts 收敛。API 返回 accepted 不等于机器 ready；超时重试不能产生两个计费资源，旧 worker 也不能在新操作后继续执行旧步骤。

本题区别于容器编排器：控制平面跨越具有独立状态、配额、故障和账单的外部 provider，许多步骤不可放进一个事务。设计覆盖租户隔离、operation、幂等 provider 身份、补偿、查询、配额与公平；底层计算和网络数据面不在范围。

## 反问与边界

先问资源类型、区域、provider 数量、创建/删除时间分布、每租户速率和最大扇出。确认 API 是同步 ready 还是异步 operation，更新是否可原地、删除能否撤销、失败如何收费，以及 provider 的幂等键、查询和补偿能力。SLO 应拆为接受延迟、到 ready、删除完成、操作结果未知年龄和账实差异。

期望状态是租户资源 spec generation 与 deletion intent；观测状态包括 operation step、provider resource ID、状态、账单与最后查询时间。resource controller 拥有 desired/observed 汇总，operation coordinator 拥有 saga step 与 epoch，每个 provider adapter 只提交匹配当前 step attempt 的事实。provider 自身才拥有物理资源真相，缓存的 `CREATING` 不是最终事实。

安全边界包含租户身份、资源归属、配额、provider credential 和审计。非目标是对不支持事务的多个 provider 声称原子瞬时创建；系统提供可恢复 saga、明确中间态和补偿。删除优先于更新还是排队，必须由 generation 规则冻结。

## 客观模型

实体为 `Resource(tenant,id,spec_generation,desired_phase)`、`Operation(op_id,epoch,type,state)`、`Step(step_id,attempt_epoch,provider_token,status)` 和 `ProviderObservation(provider_id,version,time)`。API 用租户幂等键创建稳定 resource ID 与 operation，返回 operation URL。reconciler 从 desired 与 observed 差异生成下一合法 step，持久化后才调用 provider。

不变量是资源名与 provider object 一一可追踪；同一步骤重试复用稳定 token；只有当前 operation/resource generation 能推进状态；一个租户不能耗尽全局 worker 与 provider 配额；删除完成需有 provider 证据而非本地标记。若一项资源串行经过 `k` 步、每步成功概率 `p_i`，一次无重试完成概率为 `Πp_i`，预计外部调用随步骤和重试放大。租户 t 到达率 `λ_t`、平均并发占用 `T_t`，所需公平预算近似 `λ_tT_t`。

反例：创建 VM 已在 provider 成功，但响应丢失；worker 失租约，新 worker 用新随机请求再创建，客户得到两台且都计费。稳定 provider token 或先分配可查询外部名，才能把未知结果解析成同一资源。仅把数据库 operation 设 FAILED 会掩盖真实孤儿。

## 必然约束

[DEDUCED:multi-tenant-cloud-control-plane-accepted-is-not-ready] API 接受只表示期望状态和 operation 已持久化，底层资源 provider 的异步事实未收敛前不能宣称资源 ready。provider 可能排队十分钟、部分网络完成而计算失败；同步等待只把长操作变成连接超时，不改变事实。接口必须公开 `ACCEPTED/PROVISIONING/READY/DEGRADED/DELETING` 等状态和可查询错误。

[DEDUCED:multi-tenant-cloud-control-plane-provider-effects-need-stable-identity] 外部创建响应丢失时，重试必须携带稳定资源身份和 operation epoch，否则同一租户请求可能产生两个计费资源。网络两军问题意味着调用者无法仅从超时判断未执行；provider idempotency token、确定名称或查询标签是必要交点。若 provider 无查询与幂等，只能限制自动重试并进入人工对账。

[DEDUCED:multi-tenant-cloud-control-plane-fairness-needs-per-tenant-admission] 全局队列会让单个大租户的扇出 saga 占满 worker 与 provider 配额，多租户控制面必须在入口和执行阶段都保留公平预算。租户 A 一次创建十万资源，若 FIFO 排在前，租户 B 删除一个泄露资源也被阻塞。按租户令牌、operation 类别和 provider 预算调度，删除与恢复可保留通道。

## 从简单方案演进

最简单基线是单资源类型、一个 provider、数据库 operation 表和单 worker；每步先持久化 token，再调用并查询。第一个待压测指标是 operation queue `p95` 超过 ready SLO 的百分之二十，或某租户占用超过 worker 百分之三十；此时按租户加权公平队列并按 provider/区域分池，新增配额碎片与跨池迁移。

第二个待业务校准指标是 `UNKNOWN` operation 超过一百条、最老超过十五分钟，或 orphan 扫描账实差异超过万分之一；此时暂停相关 provider 的自动创建、优先查询/对账并收紧重试。高成本资源可能一条未知就触发。provider 429 连续五分钟且错误预算 burn 超两倍时，适配器独立降速，不让重试淹没对方。

未选择跨 provider 两阶段提交，因为 provider 通常不提供 prepare/commit，伪 prepare 只是提前创建并冻结资源；若所有参与者确实提供可恢复事务接口，它才重新变优。未选择在 HTTP 请求内等 ready，只有创建稳定在数百毫秒且失败结果确定时才适合。

## 设计决定

入口鉴权、校验租户配额，以幂等键在事务中创建 resource 与 operation，返回 202。operation coordinator 获单调 epoch，把 saga 当前 step、补偿和 provider token 持久化；adapter 调用后无论响应与否都写 observation 或进入查询。resource reconciler依据当前 spec generation 和真实观察推进下一步，更新到来时产生新 generation 并明确兼容、排队或取消旧操作。

worker 租约过期后，新 worker 取得更高 step epoch。旧 worker 返回的 provider 回执不能直接推进 operation，但若携带同一稳定 token，它作为 observation 触发当前 coordinator 查询并收敛；旧随机副作用则进入 orphan 对账，不能删除可能已被新操作接管的资源。provider 更新/删除也携带资源标签和 generation fence，适配器拒绝低代命令。

反选是每个资源一个长期 actor，顺序语义简单；当资源数可控、actor runtime 持久可靠时重新变优。海量冷资源下按需 reconcile 与持久 operation 更节省成本。

## 运行与演进

SLI 包括 accepted-to-ready、operation age、按 step/provider 的错误与 429、unknown 数、orphan/账实差异、旧 epoch 拒绝、每租户队列与公平偏差、删除 age 和补偿失败。过载先限制低优先级创建与列表查询，保护删除、恢复和结果查询；provider 故障按区域隔离，不让全局重试同步。

演练：T0 W1 step epoch 7 调 provider 创建 token R；T1 provider 成功但响应丢失；T2 W1 失租约，W2 epoch 8 先按 token R 查询，得到 provider ID P，并提交 observation；T3 W1 恢复返回 P。预期不再创建，旧结果仅核对同一 P。另一轮 provider 不支持 token，超时后 operation 必须 `UNKNOWN`，自动重试受限并由 orphan inventory 解析。

待演练指标一是单租户实际 worker share 超过其配额两倍五分钟时强制公平降速；二是删除 `p99` 超过一小时或 provider 已无资源但本地仍 DELETING 超过十分钟时启动修复。阈值按资源成本和 provider SLO 校准。API/schema 演进保留旧 spec 读取，迁移 controller 也使用 generation；凭证按 provider/租户最小化并轮换，审计串联 API identity、operation 与账单 ID。

## 面试考察本质

给定“每个租户资源只能对应可追踪的 provider 事实，且旧 saga step 不能在新 generation 后制造副作用”这一不变量，因为 provider 响应会丢失、状态独立且不能加入本地事务，候选人应推导出异步 operation、稳定外部身份、可恢复 saga、查询对账和 per-tenant 公平，并按资源成本交换自动重试速度与孤儿风险。

优秀信号是区分 desired resource、operation 和 provider observation，明确 accepted 不等于 ready，画出创建成功响应丢失的时间线，并让删除/恢复拥有保留预算。常见误区是用队列宣称 exactly-once、超时即把资源标失败、为重试生成新名称，或仅在 API 入口限租户而后台扇出无限。

二十分钟回答异步 API、operation 和 adapter；四十分钟加入 saga、fencing、unknown 与公平；六十分钟讨论多 provider、账实对账、schema 演进和灾备。本题独特本质是控制面只能通过不完全的外部观察逼近现实，不能把自己的数据库状态冒充为云资源事实。
