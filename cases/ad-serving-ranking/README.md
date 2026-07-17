# 广告投放与排序

## 表面题目

设计广告投放与排序，表面流程是页面请求到达，系统找出合格 campaign、计算竞价分、展示赢家并计费。真正的状态变化包括政策与定向资格、频控、预算 reservation、唯一 impression 和结算提交；pCTR、质量分与一次请求中的候选 rank 都是派生决策。竞价最高不等于获得花费权，展示成功必须对应合规候选、可解释价格和唯一预算承诺。

题名掩盖了“排序”与“可行性”的边界。地域、同意和敏感类目先做硬过滤；频控上限同样是不可放宽的展示约束，但并发下必须在临时赢家产生后用条件 reservation 最终裁决，不能只信过滤阶段读到的旧计数。预算账本是权威状态，预测分不授予展示或花费权。本设计关注在线广告选择和预算 pacing，不把离线训练、广告素材转码或发票税务扩展成核心。

## 反问与边界

先问拍卖形式、计价、页面延迟、候选数、填充率和预算窗口；展示、可见曝光、点击哪一步计费，beacon 可能重复或丢失时如何对账。再问 campaign 日预算是否硬上限、允许多大临时超支、pacing 曲线、频控新鲜度、取消和退款。无超支承诺会把协调放进低延迟路径，必须由合同损失驱动。

还要明确 consent、地区、年龄、敏感定向、公平和品牌安全规则的 owner 与版本，不能让出价覆盖。容量按请求率乘候选特征/竞价数、同一热门 campaign 的预算冲突和频控键热点规划。日志需最小化用户属性，impression ID 可审计但不泄露跨租户预算。非目标是承诺客户端 beacon 恰好一次、用 pCTR 证明用户同意，以及事后删除已展示广告。

## 客观模型

最小命令为 `SelectAd(request,context,consent_version)`、`ReserveFrequency(user,campaign,request_id,expected_version)`、`ReserveBudget(campaign,amount,request_id,expected_epoch)`、`Commit(impression_id,reservation_ids)`、`Release(reservation_ids)` 与 `Reconcile`. campaign owner 拥有 targeting、bid、daily_budget、pacing 与 policy version；预算账本拥有 budget reservation/commit；频控服务拥有用户-campaign 的条件 reservation/commit；模型只拥有预测特征和分；账单以去重展示事件为事实。

不变量是每个计费 impression 唯一且对应合格 campaign、成功的频控槽位与预算 reservation、以及可解释价格；预算不越过声明界；频控与政策不能软化；旧进程不能在 lease epoch 后提交。硬资格集合为 `H={i | policy_i=allow && targeting_i=match}`；拍卖先在 H 中生成带价格的确定候选顺序 `A=auction(H)`，再从 A 首项开始依次尝试条件占用频控槽位与预算，第一位两者均成功者才是最终赢家。任何失败或未展示的保留都释放/超时回收。

若 `S` 个边缘各持最大预算 lease `l`，再加在途未结算额 `I`, 可声明的临时风险界近似 `S*l+I`；若合同要求严格零超支，就必须让所有有效 reservation 在单一预算序列相交。热门流量让同一 campaign 的 reservation 与频控集中，和社交 fan-out 无关。

## 必然约束

[DEDUCED:ad-serving-ranking-budget-reservation-is-authoritative-while-auction-score-is-derived] 预测分表达期望价值，不掌握花费权限。反例是 campaign C 在硬资格过滤后成为临时赢家且价格为 1 元，但预算 reservation 失败；若竞价器仍展示再异步扣费，已发生展示无法撤回。正确路径是硬资格过滤后拍卖得到候选顺序与各自价格，再为临时赢家条件保留频控槽位和对应价格的预算；任一失败就释放已占资源并尝试下一候选，只有两项均成功才允许展示与计费。若业务明确免费 house ad，无预算项可进入另一可行类，但不能用它证明付费广告也无需保留。

[DEDUCED:ad-serving-ranking-cannot-simultaneously-use-stale-edge-budget-and-guarantee-no-overspend] 两个边缘只读陈旧余额时互相不知道并发承诺。最小反例是日余 1 元，A、B 缓存均显示 1 元，在同一毫秒各赢得一次 1 元展示；异步账本只能确认已超支 1 元，不能让用户“没看见”其中一次。要保证零超支，reservation 必须强协调或使用预切且不重叠的 lease；若各边缘 lease 可同时超出剩余预算，只能声明 `S*l+I` 风险界而非零。

[DEDUCED:ad-serving-ranking-fairness-and-policy-constraints-are-feasibility-filters-not-score-features] 若 consent、地域或敏感类目只作为负权重，高 bid 可重新把禁止候选推到第一。具体反例是未同意个性化用户对 campaign X 的 policy=deny，但 X 的 bid 是普通候选百倍；再大的 penalty 只要有限就可能被抵消。硬约束必须在评分前移除且记录 policy version。只有“希望多样但允许例外”的业务偏好才适合成为软特征，法律和合同禁止项不适用。

## 从简单方案演进

最简单正确基线是单区域 owner：请求先做政策与定向硬过滤，拍卖产生候选顺序和价格；随后对临时赢家原子占用频控槽位并保留预算，失败就释放已占资源并尝试下一候选，成功后同步写唯一 impression。它容易守预算与频控但增加延迟，且热门 campaign/user 键集中。扩到多边缘时可预切不重叠小预算 lease；严格频控仍需由单 owner、条件计数器或不重叠用户分区裁决，新增闲置额度、续租、epoch fencing 与在途风险。

第一个待压测校准指标是某 campaign reservation 冲突率超过 `2%`，或按 lease 计算的近实时超支上界超过日预算 `0.1%`：缩小 lease、降低并发或切到中心保留。第二个待回放校准指标是 pacing 偏差超过目标曲线 `5%`，或频控状态 `p99` 陈旧超过 `60 s`：限制新展示并优先修复状态，不靠提高 bid 补量。两组值是合同校准起点，不是生产测量；调低会增加协调和欠投，调高会扩大超支、骚扰和合规风险。

请求量增长后先做廉价硬过滤和粗排，减少进入昂贵特征与 reservation 的候选；预算和频控按 campaign/user key 分区，热点 campaign 可独占 owner。pacing 离线给出目标速率，在线账本仍裁决每次花费。

## 设计决定

本设计在边缘执行 consent、政策和粗定向硬过滤，并用模型与竞价规则生成临时赢家、价格和后续候选顺序。对临时赢家先以 `(user,campaign,request_id)` 条件占用一个频控槽位，再按该候选价格申请带 epoch 的短预算 reservation；任一步失败都释放已成功的另一项并尝试下一候选。两项成功后才展示，随后两类服务分别以同一唯一 impression ID 幂等提交；它们不需要伪装成跨服务原子事务，提交结果不明时保留保持占用并进入对账，未展示的保留释放或超时回收。频控状态未知时 fail-closed，单纯读取旧计数不能承诺严格上限。排序在每个请求物化，不存在可跨请求审计的广告全序。

beacon 重试按 impression ID 去重，reservation 超时进入 UNKNOWN 对账而不是重新竞价；旧进程因 epoch fence 不能 commit。反选“各边缘异步记账、事后按总预算截断”，在明确允许小额超支、无硬合同上限的粗略品牌曝光中可重新变优；本题硬预算不接受。

也不选择对每个候选先做中心 reservation 再拍卖，因为大量落选保留会增加冲突且价格尚未确定；可先通过硬过滤与拍卖得到确定候选顺序，再只对当前临时赢家申请频控和预算，失败才顺延。不同拍卖规则可改变顺序与价格，不改变展示前必须取得两类保留的边界。

## 运行与演进

SLI 包括选择 `p99`、政策拒绝与版本年龄、频控陈旧、reservation 冲突和 UNKNOWN、临时超支上界、pacing 偏差、重复 impression 拒绝、填充率与对账差异。过载时先关闭昂贵预测和低优先 campaign，再缩小候选；政策、consent、频控与预算不降级。

故障演练：0ms R1 通过 consent/policy，拍卖得到 C 为临时赢家、价格 1 元；1ms 频控以当前 version 条件占用最后一个槽位，2ms 预算服务保留 C/epoch44 的 1 元；5ms 展示成功但 commit 回调丢失；10ms 客户端重试 beacon；20ms lease 过期后旧边缘再 commit。账本按 impression ID 只接受一次 epoch44，频控提交也按同一 impression 去重，旧进程因 fence 拒绝，未知状态进入对账而不创建新拍卖。若 2ms 预算失败，则释放频控槽位并尝试下一候选；若最终未展示，两类保留均释放或到期。演练并发两个同用户请求，验证条件频控只能让剩余槽位数量的请求成功。

政策和模型升级先影子比较可行集与分数，再按 campaign/地区灰度；回滚保留旧 policy 解释能力但不恢复已禁止候选。预算迁移要求旧 owner 停止发 lease、等待在途或纳入风险界，再推进 epoch。隐私删除覆盖用户特征和频控明细，账单保留依法最小审计信息；租户预算和模型数据隔离。

## 面试考察本质

给定“每次已计费展示都必须合规，且总花费不越过可声明预算界”这一不变量，因为边缘竞价器不知道其他边缘刚承诺多少预算，也不能用高分推翻 consent 与频控，候选人应推导出硬资格、预算 reservation 与派生 auction rank 的层次，并在无超支、竞价延迟、填充率和 pacing 之间取舍。

优秀回答会先定义 E 再写评分，给出双边缘余 1 元反例和 `S*l+I` 风险界，处理 beacon 去重与 UNKNOWN 对账，并把政策当硬过滤。常见误区是先展示后扣费、用缓存余额承诺零超支，或让高 bid 抵消用户同意。

二十分钟完成候选可行集、预算与一次拍卖；四十分钟加入 lease、频控、幂等和故障线；六十分钟再讨论 pacing、政策迁移、隐私与对账。追问应持续要求说明谁拥有一元余额、展示前哪个动作线性化、哪个分数只是预测。
