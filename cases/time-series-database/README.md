# 时序数据库

## 表面题目

设计时序数据库，表面上是高吞吐写入带时间戳的指标并按时间范围聚合。真正问题是：每个唯一标签组合都会分配长期状态，样本可能重复、乱序或很晚到达，而查询又需要明确何时把窗口当成最终结果。`ingest` 成功必须由负责该时间分片的接收 leader/quorum 声明样本已进入可恢复 WAL，并绑定可解释的去重规则；窗口最终性则由所有相关分片的最小 watermark 与迟到处置契约共同声明。标签索引拥有 canonical labels 到 series_id 的映射，查询层不能替接收层承诺耐久。

主流程包括标签规范化与准入、series_id 查找、WAL 追加、head chunk 更新、不可变时间块封存、压实、保留删除和查询合并。题名容易只强调压缩率，却忽略高基数先耗尽索引与内存、乱序重开旧块、保留与降采样倍增写入。本设计面向指标型时间序列，不把 request_id 等无界字段当标签，也不承诺未过 watermark 的聚合永不变化。

## 反问与边界

先问每秒样本数、活跃 series、标签值增长率、每条 series 的采样间隔、批大小、时间戳来源、重复概率、最大乱序和回填规模。查询是最近窗口、长范围聚合、按哪些标签过滤，结果需要实时近似还是最终修正；原始数据与降采样层分别保留多久。成功写需抵抗进程、节点、可用区还是地域故障，客户端超时重试使用 sample_id 还是 `(series_id,timestamp)` 冲突规则。

还需问租户基数配额、突发公平、敏感标签、数据驻留、删除时限、查询并发与成本。设备时钟可能漂移，事件时间不能直接等同于接收时间；counter 同时间戳重复和 gauge 覆盖规则也不同。后文阈值均为待校准初始策略。非目标包括按任意无界字段在线过滤、让实时面板的首次结果冒充永久最终值，以及在 WAL 未达到声明故障范围时仍返回同一种成功。

## 客观模型

状态为 `{tenant,metric,canonical_labels -> series_id}`，每个 ingest 分片保存 `{WAL, head_chunks, immutable_blocks, compaction_generation, source_epochs, shard_watermark}`，保留控制面保存各数据层的删除边界。标签索引拥有 series 身份，接收复制组拥有样本耐久记录，块目录拥有查询可见 generation。分片 `j` 的 watermark 是其所有未关闭 source epoch 的最小进度 `W_j=min_s(W_{j,s})`；相关查询分片集合为 `J` 时，再定义 `W_query=min_j(W_j), j∈J`。安静本身不能让 source 被忽略，只有生产者显式关闭 epoch，或基于有界误差的可信接收时钟触发 idle lease 并同时关闭旧 epoch，才可推进它。接口可表示为 `Ingest(series,timestamp,value,sample_id,source_epoch)` 与 `Query(matchers,start,end,as_of)`；返回结果需说明 `W_query`、结果 generation 或是否仍可修正。

不变量是已确认样本在声明故障范围可恢复；同一样本身份重试不重复计数；同时间戳冲突按 metric 类型采用确定规则；查询不会同时读取被替换块的新旧 generation；窗口只有在所有相关分片的最小 watermark 越过末端后才最终，旧 source epoch 的更晚样本必须拒绝或进入可追踪的版本化回填；保留删除不会因压实重新引入过期样本。canonical labels 的排序与编码必须一致，否则同一逻辑序列会被分裂。

活跃 series 数为 `S`，每条 head 元数据和缓冲为 `m` 字节，标签索引平均为 `i` 字节，内存 `M` 必须满足 `S×(m+i)≤M`。逻辑摄取率为 `λ`，有 `R` 个降采样层且每层写比例为 `a_j`，物理写近似 `λ×(1+Σa_j)`，再乘复制和压实因子。乱序率会让已封存块重开或产生补丁块，故相同 `λ` 下成本也可完全不同。

## 必然约束

[DEDUCED:time-series-database-label-cardinality-is-a-state-allocation-commitment] 标签不是普通列过滤条件。最小反例是 `http_requests_total{service="api",request_id="uuid"}` 每分钟出现一百万个 UUID；即使每条只有一个样本，仍生成一百万条 series。若每条 head 加索引约二 KiB，就立即需要约一点九 GiB 状态，尚未计 WAL 与副本。提高块压缩率不能收回接收时已经分配的身份和内存，因此基数必须在准入前估计、限制或把无界字段移到日志。

[DEDUCED:time-series-database-ingest-acknowledgement-must-bind-deduplication-and-durability] 客户端发送样本后响应丢失，它不知道首写是否进 WAL；重发可能造成 counter 重复，放弃又可能丢样。只有同一 sample_id 的幂等记录，或明确的 `(series_id,timestamp)` 冲突规则，加上可恢复 WAL 边界，才能让两次结果收敛。若 leader 只在内存中确认后崩溃，去重状态和样本一起消失，新的 leader 仍无法区分重试，因此持久性与去重不能分开声明。

[DEDUCED:time-series-database-window-finality-depends-on-a-lateness-bound] 一分钟窗口在分片 A 首次计算为九十，但相关分片 B 的 source 已安静且 watermark 仍停在十一点五十九分；即使 A 已到十二点零三分，`W_query=min(W_A,W_B)` 仍未越过窗口末端，结果不能称为最终。安静 source 只有显式关闭 epoch，或在可信且误差有界的接收时钟上触发 idle lease、关闭旧 epoch 后，才不再无限阻塞。允许晚到 `L` 时，“约在 `end+L` 关闭”还依赖该可信时间和每个 source 的事件时间约束；关闭后到达的旧 epoch 样本只能拒绝，或进入产生新 generation 的版本化回填，不能静默改写已声明最终的结果。

## 从简单方案演进

最简单正确基线是单机按 series 追加 WAL 与内存 head，周期封存按时间排序的不可变块，查询合并 head 和块。数据超过单机容量或机器故障目标后，按 tenant/series 哈希分片并复制 WAL；查询按时间与标签路由。只有长范围查询重复消耗原始样本，才增加 rollup 和多保留层；只有明确接纳晚到，才引入补丁块或独立回填路径。

第一个**待校准**切换指标是：单租户活跃 series 连续十分钟超过配额百分之八十，或某标签每小时新值超过十万，拒绝新组合并返回 cardinality 报告，将 request ID 等字段转移到日志或 trace。百分之八十与十万需按 `m+i` 实测、租户增长和扩容提前期校准；只增加机器会延后但不消除无界增长。

第二个**待校准**切换指标是：乱序样本比例超过百分之二，且 compaction backlog 预计清空时间超过三十分钟，将实时接收乱序窗口收紧为五分钟，较晚数据路由到独立修正流；若业务必须接受大规模历史回填，使用隔离分区和查询 as-of generation。百分之二、三十分钟和五分钟需要按块大小、修正价值和实时 SLO 校准。

未选择为每个样本建立全局有序日志，因为跨 series 不需要单一顺序，全球协调会把时间戳吞吐变成一个瓶颈。若业务是严格有序审计事件而非指标，日志系统会重新变优。也未选择所有查询即时扫描原始样本；若数据规模小、查询稀少且正确性优先，它更简单，只有重复聚合成本越界时 rollup 才值得其修正复杂度。

## 设计决定

本设计先规范化 metric 与标签并执行租户基数准入，再查找或创建 series_id。批次按 series 分片送到 ingest leader，使用 sample_id 或声明的同时间戳策略去重，追加到跨故障域 WAL 并达到所选确认级别后响应。每个 source epoch 通过事件进度或显式 close 推进分片 watermark；idle lease 只使用可信接收时钟，并使该旧 epoch 后续样本按拒绝或版本化回填处理。查询协调器对所有相关分片取最小 watermark。head 按允许乱序窗口缓冲，封存为不可变块；watermark 前的晚到写补丁，最终边界后的数据进入隔离回填并发布新查询 generation。块目录以 generation 原子发布压实结果。

查询先按标签索引取得 series，再裁剪时间块，合并 head、基础块和当前补丁 generation。实时结果携带跨相关分片的 `W_query`；最终查询默认只覆盖 `W_query≥window_end` 的窗口，并返回结果 generation。超时重试保持批次和样本身份。过载时先停止低价值 rollup 与宽范围查询，限制新 series 和回填，再按租户公平摄取；不能丢弃已确认 WAL、把 idle source 无条件排除，或静默扩大去重覆盖规则。

反选方案是把所有标签和值放进通用列存储并依赖普通二级索引。它简化组件，却无法在接收前对 series 状态承诺做准入，也难以利用时间块与保留裁剪。若数据低基数、写入低且任意维度分析比实时摄取重要，分析列存会重新变优；本设计为高频指标把身份索引与时间块明确分开。

## 运行与演进

SLI 包括确认摄取延迟、WAL 未复制字节、去重命中与冲突、活跃 series、标签新值率、head 内存、乱序分布、各分片与 source watermark 落后、idle epoch 数、最终后拒绝/回填数、compaction/backfill backlog、查询扫描块数、保留删除延迟和按租户拒绝数。容量预警同时看 `S` 和增长导数；平均样本压缩率良好并不能证明 head 安全。

故障演练时间线：零毫秒客户端发送 sample_id 77；十毫秒样本达到 WAL quorum；十五毫秒响应丢失；二十毫秒 leader 崩溃；四十毫秒新 leader 从 WAL 恢复去重状态；客户端重发 77，系统返回原结果且只计一次。另一轮让相关分片 A 的 watermark 到十二点零二分、B 因一个 idle source 停在十一点五十九分，验证协调器仍以十一点五十九分为 `W_query`；随后用可信时钟关闭 B 的旧 epoch 才推进最终边界。旧 epoch 样本再到达时，验证它被拒绝或进入回填并产生新查询 generation，而非悄悄改写已声明最终的结果。

标签 schema 迁移先让查询识别新旧规范化规则，再双索引并比较 series 映射，最后停止旧编码；回滚保留读取新 series_id。保留策略先写 delete horizon，再让 compaction 移除，备份和降采样层都需遵守。多地域复制若异步，成功语义只承诺本地域故障域；提升为地域耐久要等待远端 WAL 并接受 RTT。敏感标签要白名单化、散列或加密，避免索引和查询日志泄露身份。

## 面试考察本质

这题考察的是：给定“确认样本可恢复且聚合最终性由跨相关分片最小 watermark 与迟到处置共同界定”的不变量，因为接收节点无法预先知道无界标签将分配多少状态、查询也不能把安静 source 当作已结束，候选人能否把基数准入、去重耐久、可信时间、idle epoch、版本化回填、保留与压实放大变成可执行取舍。

优秀回答会把 series 身份与普通字段区分，用一百万 UUID 的反例量化内存，说明 ingest ack 同时约束 WAL 和去重，并把相关分片取最小、idle source 关闭、迟到拒绝或版本化回填写进窗口契约。常见误区是只谈压缩、无条件用 `end+L` 推导最终、把安静 source 忽略、允许任意标签后再报警，或让首次 dashboard 数字无条件称为最终。

二十分钟完成 series、WAL、head、块和确认语义；四十分钟加入标签索引、乱序、watermark、压实和保留；六十分钟再讨论回填、rollup、多租户、地域和迁移。追问应落在某个样本响应后：它在哪个故障域可恢复，重发怎样去重，所属窗口何时关闭，以及新增一个标签值究竟分配了多少长期状态。
