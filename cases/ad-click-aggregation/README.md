# 广告点击聚合

## 表面题目

设计广告点击聚合，表面是接收曝光和点击事件，按 campaign、creative 与时间窗口计算点击数、展示数和 CTR。真正的状态变化是一个带稳定 `event_id` 的业务事实进入特定归因版本与事件时间窗口，并在去重边界内至多计费一次。成功必须区分实时看板的新鲜度、允许修正的统计口径和账单最终性；三者不能共用一个“当前累计数”。题名也掩盖了曝光与点击跨设备、跨分区乱序，以及处理器重放会把已见事件再次送到聚合状态的问题。

## 反问与边界

先问哪些事件参与计费、归因窗口多长、点击和曝光的 event time 由谁签发、最大允许迟到多久、去重 ID 的稳定范围，以及欺诈过滤和规则更新是否重算历史。SLO 应分别覆盖事件接受、provisional 看板新鲜度、final 窗口关闭和账单修正时限。容量用峰值事件率、唯一 ID 率、campaign/creative 基数、滑动窗口重叠数、重放时长与热点 campaign 表达，不能只看平均点击率。

排序域是每个 source partition 的摄入顺序；跨 source 的业务时间只能用 watermark 与归因规则关联，不存在全局到达序。重放边界由原始日志 retention、最后完整 checkpoint 和存活的 dedup horizon 共同决定。背压在 source credit、解析、按 key shuffle、状态后端与报表 sink；state owner 是 `(campaign,window,attribution_version)` 的分片状态与原始事件仓。丢失可能发生在客户端未持久上报、source retention 到期或迟到政策丢弃；重复发生在 SDK 重试、checkpoint 回退与 sink 提交未知。

## 客观模型

最小事件为 `Impression(event_id,user_key,campaign,event_time,ingest_time)` 与 `Click(...)`，查询为 `GetAggregate(campaign,window,version,finality)`。原始事实携带归因、过滤和 schema version；聚合状态保存计数、去重集合、watermark、窗口状态 `OPEN/PROVISIONAL/FINAL/CORRECTED` 与输出版本。原始可重放事件和规则版本是权威，dashboard 行是派生；账单 owner 只接受冻结版本或显式 correction。

不变量是：在声明 dedup horizon 内，同一有效点击 ID 对同一归因版本最多计一次；分子分母共享过滤与归因版本；迟到或规则修正不得静默覆盖已结算金额；旧 checkpoint owner 不能提交新 epoch 输出。窗口 w 的精确去重状态约为 `M≈U_w×(id_bytes+metadata)`，其中 `U_w` 是 horizon 内唯一事件数。滑动窗口长度 `W`、步长 `s` 时，一条事件可能更新 `ceil(W/s)` 个窗口，状态和 shuffle 随之放大。

CTR 是 `clicks/impressions`，但仅在相同事件范围和版本下有意义。一个热门 campaign 可能把所有事件汇入同一 key，平均分区吞吐无法消除它的串行更新下界。事件时间决定业务窗口，处理时间只反映系统看到事件的时刻；watermark 是基于各 source 进展的关闭策略，不是真实世界不会再有旧事件的证明。

## 必然约束

[DEDUCED:ad-click-aggregation-dedup-horizon-bounds-count-correctness] 去重表回收 `event_id=C` 后，系统仅凭再次出现的 C 无法知道它是迟到重试、checkpoint 重放还是新生成的冲突 ID。最小反例是 checkpoint 回退一小时，而去重状态只保留三十分钟：同一点击被重新累加，CTR 与费用同时上升。结论是“无重复计数”只能覆盖去重 horizon，并要求原始事件 ID 稳定；超出边界的重放必须重建完整窗口或输出可审计修正，不能宣称天然 exactly-once。

[DEDUCED:ad-click-aggregation-event-time-window-needs-a-lateness-policy] 点击 C 的 event time 为 10:00:58，在 10:01:00 到达；对应曝光因分区延迟到 10:01:20 才到。按处理时间分钟关窗会把两者拆到不同窗口，单有时间戳却无 watermark 也无法判断何时释放状态。系统必须选择 allowed lateness、source idle 和 late side output；等待更久提高正确覆盖但增加状态与最终性延迟，立即关闭则接受明确修正或丢弃。

[DEDUCED:ad-click-aggregation-billing-finality-is-not-live-dashboard-freshness] 实时看板为追求秒级新鲜会在所有迟到事件到齐前显示值，账单却要求口径冻结且可追溯。最小反例是广告主在 10:05 看到 100 次点击，10:20 到达 5 次合法迟到点击；直接覆盖同一行会使已导出的账单无法解释。应发布 provisional 版本并在 final 后只追加 correction。若业务是低价值趋势且不结算，允许最终覆盖可简化，但不能沿用计费承诺。

## 从简单方案演进

基线是单进程按处理时间维护每 campaign 总计数。它低延迟，却没有窗口重放、去重和归因解释。当 SDK 重试使同 ID 重复率达到计费风险上限，先保存 event ID 与原始日志；这解决重复上报，却引入随 horizon 增长的状态。当去重与窗口 state 超过分区内存预算的 70%，或新 unique key 速率超过安全 checkpoint 吞吐，按 campaign 分层预聚合、扩 state backend，或只对非计费指标采用有误差界的近似，计费路径不得静默降精度。

跨分区乱序出现后，引入 event-time watermark、allowed lateness 和版本化关窗。若 lateness 外点击超过总点击 0.5%，或 watermark `p99` lag 超过报表新鲜度预算的 50%，先定位 source 时钟与 idle 分区，再延迟 final 或生成 correction；资金风险高时隔离异常 source，不能直接丢弃。checkpoint 恢复与 sink 输出以 epoch 对齐；不支持幂等提交的外部报表仍可能重复写。

70%、0.5% 和 50% 是待回放测试、故障演练与业务风险校准的初始阈值。调低会更早扩状态或推迟结算，调高减少资源但扩大重复收费与修正风险。反选“只保留最终累计总数”在没有时间归因、无重放、仅做低价值展示统计时重新变优；计费和预算控制需要窗口版本与原始 lineage。

## 设计决定

本设计先将原始事件持久化，再按 campaign 与 event time 路由到 keyed state。处理器在同一 checkpoint 中保存 source offsets、dedup 集合、窗口计数、watermark 与规则版本；sink 以 `(window,version,checkpoint_epoch)` 幂等 upsert。provisional 可以多版本刷新，final 只由冻结归因规则产生，后续合法迟到事件写 correction 而非改写历史。排序只保证单 source partition 和同 key 算子内序列。

source credit 与 bounded shuffle 让 sink 变慢时背压返回入口；优先暂停非计费派生维度和历史 replay，不能为维持新鲜度丢计费事实。checkpoint ACK 丢失时从最后完整 epoch 重放，dedup state 若仍覆盖输入则跳过；覆盖不足时标记整个窗口重建，不把部分结果称作正确。外部账单若不能按事件 ID 原子去重，只能承诺至少一次 correction。

未选择所有维度一次性精确 group-by，因为高基数会让状态乘积无界；长尾探索走离线明细，实时计费保留受控维度。按处理时间微批在乱序可忽略且不结算的看板更简单，但不适用于跨 source 归因。

## 运行与演进

SLI 包括摄入延迟、watermark lag、late-event 比率、去重命中与过期后重放量、state 字节、checkpoint 时长、热点 key 负载、provisional-to-final 延迟和 correction 金额。CTR 看板同时显示数据版本、分子分母覆盖与水位，不用一个百分比掩盖缺失曝光。租户按 campaign 数、窗口状态和查询维度配额；用户标识做最小化、轮换或匿名化，原始明细与结算数据使用不同保留期。

故障时间线：0 min，watermark 推到 10:05，10:00 窗口发布 provisional v1；2 min，checkpoint 损坏并从 09:55 重放；3 min，若 09:55–10:05 dedup state 完整则跳过已见 ID，否则撤销候选并重建；8 min，迟到 C 到达，在 allowed lateness 内发布 v2，超出则进入 correction。sink 在写 epoch 42 后 ACK 丢失时按相同 epoch 查询或重试，不能创建无关联重复行。

规则升级先固定 attribution v7 和输入快照，影子计算 v8 差异，再让新窗口使用 v8；旧账单 correction 仍引用 v7。回滚只切换后续版本，不覆盖已发布 final。演练应证明 checkpoint 恢复、迟到修正和热点 campaign 分片时都保持事件 ID、窗口和账单版本可追踪。

## 面试考察本质

给定“同一可计费点击在声明归因窗口内只能计一次”的不变量，因为处理器不知道晚到事件是否仍会出现，也不知道重放事件是否已被回收的去重状态遗忘，候选人应推导出原始事实、dedup horizon、event-time 关窗和账单冻结四层。主导取舍是状态成本与延迟最终性，而不是把一个流框架的 exactly-once 当业务账单证明。

优秀信号包括写出去重内存公式、区分 event time 与 ingest time、解释 watermark 和 allowed lateness、让 provisional 与 final 分版本，并指出 sink 原子边界。常见误区是按处理时间累计、只去重点击不去重曝光、分子分母使用不同规则，或清理 state 后仍承诺无限重放无重复。

二十分钟回答应完成事件、窗口、归因和原始日志；四十分钟加入去重状态、watermark、checkpoint 与修正；六十分钟再讨论热点 campaign、近似统计、反欺诈版本、隐私和账单对账。追问用 10:00:58 点击、迟到曝光和 checkpoint 回退，要求逐一说明排序域、重放边界、背压、state owner、丢失与重复。
