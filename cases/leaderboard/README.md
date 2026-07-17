# 排行榜

## 表面题目

设计排行榜，表面流程是玩家得分后立即看到自己和附近玩家的名次，比赛结束后系统按最终排名派奖。真正的状态变化是带身份的计分事件进入某个比赛规则版本，分数投影随之更新，关闭边界后再生成可审计快照。实时 rank 是派生读模型；一旦名次决定奖金或晋级，冻结快照才成为权威裁决。

题名掩盖了实时展示与最终结算的不同承诺。页面可以容忍几秒滞后和 provisional，奖金不能因缓存、迟到事件或反作弊重算而有两个答案。本设计关注单赛事计分、同分规则和封榜，不把匹配系统、游戏内状态或支付实现混入排名所有权。

## 反问与边界

先问分数是绝对值还是增量、由谁签发、是否可撤销，同一玩家更新频率、参赛人数与头部热点。再问实时榜延迟、附近排名与 Top-N 查询、同分 tie-break、比赛关闭时区、迟到事件接受范围和最终快照何时不可更改。若反作弊可在赛后推翻分数，页面与派奖要明确 provisional 窗口。

地域上需决定 score event 的单一入口序还是可合并分区；奖励价值决定关闭时的协调成本。安全上客户端不能自报可信分数，事件需签名和幂等键，查询要防枚举未公开玩家。容量按事件率、参赛人数、排序索引更新、快照字节和重算比例规划。非目标是让缓存直接派奖、用墙钟最后写胜，以及承诺实时榜每毫秒全局同步。

## 客观模型

最小命令为 `RecordScore(contest,player,delta,event_id,source_seq)`、`CorrectScore(event_id,reason)`、`CloseContest(expected_rules_version)`、`FinalizeContest(close_epoch,correction_cutoff)` 与 `GetRank(contest,player,view_version)`. 计分日志拥有可接受事件与撤销历史；比赛状态拥有 `rules_version`、`close_epoch`、关闭水位线、`correction_cutoff` 与单调 `finalize_epoch`；实时投影拥有分数与 rank；结算器拥有签名冻结快照。

不变量是同一 event 最多计一次；分数可由相同规则下事件重放；tie-break 完全定义稳定顺序；关闭快照只包含关闭水位线内、且在 `correction_cutoff` 前裁决的有效事件；比赛只能从 `CLOSED` 一次性 CAS 到 `FINALIZED(finalize_epoch,snapshot_id)`，此后不能再分配竞争纪元或替换奖金快照，派奖只读该 ID。截止后的更正不得原地改写奖金榜，只能被拒绝进入本次结算，或经独立治理生成补偿记录。事件序、最终化纪元和冻结顺序权威，实时排序树与附近榜派生。

`score(p,c)=Σ delta(e)`，排序键为 `(score DESC,tie_break ASC,player_id ASC)`。若末段到达率 `λ` 大于投影处理率 `μ` 并持续 `t`，关闭前积压下界为 `max(0,(λ-μ)*t)` 个事件。热门赛事和头部玩家的更正会集中在同一 contest 分区；全量重排与快照则随参赛人数增长。

## 必然约束

[DEDUCED:leaderboard-live-rank-is-derived-while-prize-rank-requires-a-frozen-authority] 实时投影只知道已消费前缀，不知道迟到事件和未完成反作弊。最小反例是关闭命令已提交到 epoch 1000，投影只到 980；页面显示 A 第 10，随后 sequence 995 使 B 超过 A。如果派奖器读取不同时间的缓存，同一奖励可有两个赢家。系统必须声明关闭水位线与 `correction_cutoff`，在截止后以单调 `finalize_epoch` 生成 `S(rules_v,close_epoch,input_boundary,correction_cutoff,finalize_epoch)`，派奖固定读取 S；实时榜可继续但必须标 provisional。

[DEDUCED:leaderboard-score-event-idempotency-precedes-rank-maintenance] 排名树上的增量不保留事件身份，无法判断网络重试。反例是 A 有 99 分，e1 加 1 在两个地域重复到达；两次 `ZINCRBY` 得 101，而权威事件只能计一次。事后只看总分无法知道哪一分应删除。先以 `(contest,event_id)` 唯一接受，再投影 score 和 rank，才能重放与纠偏。若来源提供单调绝对分值，也仍需 source_seq 防旧值覆盖新值。

[DEDUCED:leaderboard-tie-breaker-is-part-of-the-ordering-contract] 两名玩家同为 100 分时，只按 score 排序会让不同排序树按内部遍历返回不同第 10 名。具体反例是奖励只给前十，A 与 B 同分争最后一位，区域甲返回 A、乙返回 B；前端再排序无法修复已派奖决定。必须把达分事件序、完成时间或稳定玩家 ID 写进规则版本。若业务允许共享名次和共同奖励，可以不打破平手，但奖励数量与预算也必须按该语义设计。

## 从简单方案演进

最简单正确基线是在一张比赛表中条件接收幂等事件，每次查询聚合分数并排序。参赛小、更新少时容易审计；事件和查询增多后，读取全量排序越过延迟预算，于是异步维护分数表与有序索引。新增投影滞后、缓存版本和重建过程，实时结果必须携带 watermark。

第一个待压测校准指标是距离关闭不足 `60 s` 且投影 watermark 落后事件日志超过 `2 s`：停止宣传“实时即最终”，给封榜消费者优先级并限制动画刷新。第二个待运营校准指标是赛后反作弊撤销/更正超过 `0.1%`，或其 `p95` 完成时间超过 `correction_cutoff` 窗口的一半：延长下一期的 provisional 阶段并推迟 `correction_cutoff`，但已发布 `finalize_epoch` 不回开。两组值只是初始策略；调低会增加结算等待与保留容量，调高会扩大错奖和申诉风险。

赛事数量增大时按 contest 分片，单赛事的关闭序和规则仍由一个 owner 管理；超大赛事可对事件预聚合，但冻结时必须汇合到确定输入边界。历史榜转为不可变快照和离线索引，实时结构只保留活跃赛事。

## 设计决定

本设计让计分日志条件接受签名 event，投影器按 contest 顺序更新 score 与有序索引，查询返回 `projection_watermark` 与 provisional 标志。关闭先提交 `close_epoch` 并停止接收边界外普通事件，追平声明水位线；反作弊只能在独立 `correction_cutoff` 前追加更正。截止到达后，结算器分配一个 `finalize_epoch`，生成签名快照，并用 CAS 将 `CLOSED` 一次性推进到携带该快照的终态 `FINALIZED`；失败的并发结算器丢弃候选快照，终态不能重新打开。派奖与晋级永远绑定该 snapshot ID。

事件超时重试返回原接受结果，更正以引用原 event 的新事件表达，不原地改历史。`correction_cutoff` 后到达的更正明确返回 `TOO_LATE_FOR_FINAL`，保留为申诉证据但不进入奖金快照；若业务决定补偿，只创建引用原 snapshot 的独立补偿裁决和付款，不生成第二个“最终名次”或改写已派奖 snapshot。投影故障可从日志重建；缓存不可用只影响展示。反选“每次得分同步维护所有玩家的精确全局排序”，在小赛事、更新稀少且每次读都必须强一致时重新变优；通常它把排序索引尾延迟放进计分关键路径。

也不选择关闭时直接复制某个缓存，因为其 watermark 和规则版本可能未知。若反作弊永不更改且所有 event 在关闭前同步提交，冻结可更快，但仍需显式 input boundary 与 tie-break。

## 运行与演进

SLI 包括 event 重复拒绝率、投影 lag、实时 rank 年龄、score 重放校验差异、关闭追平时间、provisional 时长、快照生成与派奖 snapshot 不匹配数。过载时先降低附近榜刷新和动画，再限制非关键历史查询；计分接受、关闭边界与快照校验优先。

故障演练：关闭前投影到 980，关闭命令提交 epoch 1000；迟到消费者先显示包含 1002 的临时榜，反作弊在 `correction_cutoff` 前撤销 995，结算器随后提交 `finalize_epoch=7` 并生成 S7。截止后又收到对 970 的更正时返回 `TOO_LATE_FOR_FINAL`，派奖任务即使重试也只读 S7；必要补偿引用 S7 单独审批，不改名次。页面把边界外变化显示为下一期或 provisional。演练验证同一最终化 CAS 不能生成 S8 作为竞争奖金榜。

规则升级为新 contest_version，旧赛事继续按旧 tie-break；灰度先影子重放并比较快照摘要。跨地域只复制日志与快照读取，写 owner 通过纪元迁移。玩家删除与公开名隐藏不破坏审计 event，展示使用匿名标识；租户赛事隔离分区与结算密钥。

## 面试考察本质

给定“同一规则与关闭边界下的奖金名次必须唯一、可审计”这一不变量，因为实时投影不知道迟到事件和反作弊结论，候选人应推导出幂等计分日志、派生 live rank 与冻结快照三层，并在实时体验、封榜延迟和错奖风险之间决定最终性窗口。

优秀回答会定义 tie-break，写出 `max(0,(λ-μ)t)` 积压，用重复 e1 证明不能直接增量排名，并让派奖绑定快照。常见误区是把缓存榜当事实、依赖客户端时间封榜，或在同分时说“数据库自然稳定”。

二十分钟完成事件、分数和排序契约；四十分钟加入投影、封榜、重放与故障线；六十分钟再讨论反作弊、超大赛事、隐私与规则迁移。追问应固定在关闭前后每个 sequence 属于哪个快照、用户看到的 rank 是否 provisional。
