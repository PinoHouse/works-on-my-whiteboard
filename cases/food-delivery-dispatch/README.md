# 外卖配送调度

## 表面题目

订单确认后，系统结合餐厅备餐时间、骑手当前位置与已有停靠计划，把取餐和送达加入某位骑手路线。成功不是把订单交给离餐厅最近的人，而是满足容量、取送先后和已承诺时间窗，并在备餐延迟、取消和交通变化时受控重排。与网约车一单一车不同，外卖的核心放大来自一名骑手携带多单，新增订单会改变整条路径。

## 反问与边界

先问是否允许拼单、最大容量、每单必须先 pickup 后 dropoff、冷链/车型等硬约束；承诺的是预计时间还是硬截止。确认备餐预测误差、调度批窗口、p99、区域峰值、餐厅热点、骑手位置 max_age、取消和拒单语义。优化目标可能包含超时惩罚、空驶、餐厅等待与公平，权重需业务校准。订单支付与库存不在本题，但其取消事件必须版本化输入；路线服务也不能把预测备餐完成当已备好事实。

权威源分属不同 owner：order owner 持有订单版本、取消与唯一 assignment reservation，courier owner 持有计划版本、容量和停靠序列，餐厅 ready 事件是带版本输入；空间/ETA 候选、备餐预测和组合搜索都是派生估计。order 与 courier 不在同一真实事务域时，系统只能用 assignment token 的幂等 propose/commit 与补偿对账收敛，不能声称一次 plan CAS 同时原子更新订单。刷新更快需要更密位置、餐厅信号和频繁求解，并可能让计划抖动。

## 客观模型

命令为 `ReserveAssignment(order_id,expected_order_version,courier_id,token)`、`ProposePlan(courier_id,expected_plan_version,token,stops)`、`CommitPlan(token)` 与版本化取消。订单状态为 `confirmed/unassigned(version)`、`reserving(token,courier,version,expires)`、`assigned(token,courier)`、`picked_up`、`delivered/cancelled`；计划中的 stop 先是 `tentative(token,order_version)`，确认后才是 `active(token)`。一单唯一分配的线性化点是 order owner CAS `unassigned -> reserving(token,courier)`，courier plan CAS 只保证其本地容量与 stop 版本，二者不是一次原子提交。相同 token 的重试必须返回同一决定，不同 token 不能覆盖仍有效的 reservation。

对候选计划 P，代价可写 `J(P)=Σ late_penalty_i + α*travel_time + β*restaurant_wait + γ*replan_churn`。将一个订单插入 n 个 stop 有约 `O(n²)` 个 pickup/dropoff 位置组合；多个订单全局最优迅速组合爆炸。承诺可行需对每个前缀验证 `0≤load≤capacity`，且 pickup 在对应 dropoff 前。

## 必然约束

[DEDUCED:food-delivery-dispatch-nearest-courier-is-not-earliest-delivery] 外卖完成时间同时受备餐、取餐等待和后续停靠约束，距离餐厅最近的骑手不等于最早交付。骑手 A 距餐厅 1 分钟但已有三单，B 距 4 分钟且空闲；若 A 的后续 detour 15 分钟，按距离会更晚。

[DEDUCED:food-delivery-dispatch-insertion-must-validate-whole-plan] 新订单插入多单路线时必须重新验证整条停靠序列的容量和时间窗，局部增量最短不能证明既有承诺不被击穿。把新取餐插在最短位置可能使旧订单晚 8 分钟；只有重放所有 stop 的到达、load 和先后才能接受。

[DEDUCED:food-delivery-dispatch-assignment-needs-order-owner] order owner 的 reservation/version CAS 必须是唯一分配决策，courier plan 只能用同一 token/version 做 propose 与 commit，否则跨 owner 并发会产生双分配或幽灵容量。反例：两个优化器都读到订单 O 未分配，分别在骑手 A、B 的不同 plan owner 上 CAS 成功；两个 plan CAS 互不冲突，无法证明 O 只归一人。必须先让一个 `(order_version,token,courier)` 在 order owner 赢得 reservation，输家停止；赢家再用同一 token 对 courier plan propose，失败则按 token 补偿订单 reservation 和 tentative stop，不能把两个 owner 说成“同时更新”。

## 从简单方案演进

不拼单时可为每单选择满足资格且 ETA 最小的空闲骑手，类似一对一派单。当骑手利用率低、等待订单持续高于业务阈值时允许两单插入；每次枚举现有 plan 的合法 pickup/dropoff 位置。若求解 p95 超调度预算或 stop 数增加导致 `n²` 爆炸，限制最大 stop、候选骑手和搜索时间，返回当前最好可行解而非未证明最优。

逐订单贪心会错过微批组合；当同餐厅/同方向订单比例高且额外等待批窗口小于预期节省，可按一到数秒微批优化。批窗口提高组合质量却直接增加首单等待。无论单条还是微批优化，求解结果都只是建议：每个订单先争用 order owner reservation，再以 token/version 串联 plan propose，不能让两个优化器凭不同 courier plan CAS 各自宣称成功。备餐预测 age/误差过大时，以 ready 事件门控远端骑手出发或留安全裕量；更频繁重算会增加通知、路线认知和 CAS 冲突。

未选全城全局最优求解，因为输入在求解时已变化且组合成本过高；离线规划或低频长周期配送可重新变优。未选“永远不拼单”，在供给充足、准时优先或高价值订单下它反而应由策略选中。

## 设计决定

选择区域候选、有限插入搜索、order reservation CAS 与 token 化 courier plan 状态机。确认订单进入待调度，结合备餐窗口选若干骑手；对每个当前计划枚举受限插入，逐 stop 验证先后、容量、时间窗与硬资格，再比较 J。选定候选后执行跨 owner saga：

1. order owner 以 expected order version CAS `unassigned -> reserving(token,courier,expires)`；这是唯一 assignment 决策，两个优化器只有一个能赢。
2. 赢家在 courier owner 以 `expected_plan_version,token,order_version` CAS 加入 tentative stops 并预占容量；plan 已变化或整条序列不再可行就拒绝。
3. propose 成功后，order owner 仅在 token/version 匹配时推进为 assigned；courier owner 再幂等 `tentative(token) -> active(token)`。中途响应丢失可查询两端并按 token 重试，不生成新 token 覆盖旧状态。
4. propose 失败时，协调器在 order owner 将同 token 的 reserving 补偿为新的 unassigned version，并删除同 token tentative；如果任一进程崩溃，对账器以 order owner 决策为准：assigned 就补齐 plan commit，cancelled/unassigned 就释放 tentative，reserving 超时则先由 order owner 决定重试或释放。另一端不可达时保持 reservation 隔离，不猜测成功。

picked_up 后取消转为异常处置而非简单删除；未取餐取消在 order owner 与 reserving/assigned 竞争版本，取消获胜后按 token 补偿 plan，旧优化器的 propose/commit 因 order version 不匹配被拒绝。备餐延期与骑手状态变化都从最新 plan version 重算，不能覆盖新 stop 或保留幽灵容量。求解超时返回最佳已验证可行计划；没有可行解则排队或拒绝承诺，不能返回未验证路径。未选择派生路线缓存作为权威。

## 运行与演进

SLI 包括确认到分配、ready 到 pickup、按时送达率、备餐预测误差、每单 detour、餐厅等待、order reservation 冲突、plan CAS 冲突、reserving/tentative age、补偿与对账积压、replan 次数、容量违规数与每骑手负载公平。过载先减少候选和搜索深度，再关闭低收益拼单，最后收紧接单范围；硬容量、时间窗、order winner 与 plan token 验证不降级。

故障时间线：12:00 优化器 A、B 都读到订单 O v8 未分配，分别选骑手 C、D；12:00:01 A 在 order owner 以 tokenA 赢得 `v8 -> reserving v9`，B 的 tokenB CAS 失败，不能因 D 的 plan 仍可写就继续；12:00:02 C plan v12 接受 tentative(tokenA)，协调器崩溃；对账读取 O 仍 reserving 和 C 的同 token tentative，重试 order assigned 与 plan commit，最终只有 C 获得 O。另演练 O 在 propose 前取消，旧 token 因 order version 变化被拒并释放；演练餐厅 ready 比预测晚十分钟，比较不重排和重排的既有订单影响。灰度新成本函数只影子评分并记录决策分歧。压测指标是求解 p95 超批窗口一半、order/plan CAS 冲突超百分之五、tentative age 越界；业务校准迟到惩罚、最大 detour 与拼单容量。

## 面试考察本质

本题本质是：给定“一个订单只有一个 assignment、整条取送计划始终满足容量、先后与已承诺时间窗”的不变量，因为订单与骑手计划属于不同 owner，备餐完成、交通与取消又只能以滞后事件获知，候选人必须推导 order reservation 作为唯一决策、token 化 plan propose/commit、失败补偿与有限组合搜索，再按准时率、reservation 占用、利用率与求解成本决定拼单。

优秀回答会把备餐时间纳入 ETA，验证整条计划而非局部距离，用两个优化器写不同 courier owner 的反例说明为什么 plan CAS 不能选出 order winner，并画出中途崩溃后的补偿状态机。常见误区是套网约车最近司机、声称跨 owner 同时提交或全局最优、取消直接删缓存。追问可进入多餐厅、多温层、微批窗口、预测置信度和骑手公平。20 分钟讲状态、插入与 order owner，40 分钟补 token saga 和取消/延期，60 分钟讨论近似求解、灰度成本函数和城市级容量规划。
