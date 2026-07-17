# 时序数据库

## 表面题目

设计时序数据库，表面上是高吞吐写入带时间戳的指标并按时间范围聚合。真正问题是：每个唯一标签组合都会分配长期状态，样本可能重复、乱序或很晚到达，而查询又需要明确何时把窗口当成最终结果。`ingest` 成功必须由负责该时间分片的接收 leader/quorum 声明样本已进入可恢复 WAL，并绑定可解释的去重规则；窗口最终性则由所有相关分片的单调 watermark floor 与迟到处置契约共同声明，source epoch 的开关不能让已声明的最终 horizon 回退。标签索引拥有 canonical labels 到 series_id 的映射，查询层不能替接收层承诺耐久。

主流程包括标签规范化与准入、series_id 查找、WAL 追加、head chunk 更新、不可变时间块封存、压实、保留删除和查询合并。题名容易只强调压缩率，却忽略高基数先耗尽索引与内存、乱序重开旧块、保留与降采样倍增写入。本设计面向指标型时间序列，不把 request_id 等无界字段当标签，也不承诺未过 watermark floor 的聚合永不变化。

## 反问与边界

先问每秒样本数、活跃 series、标签值增长率、每条 series 的采样间隔、批大小、时间戳来源、重复概率、最大乱序和回填规模。查询是最近窗口、长范围聚合、按哪些标签过滤，结果需要实时近似还是最终修正；原始数据与降采样层分别保留多久。成功写需抵抗进程、节点、可用区还是地域故障，客户端超时重试使用 sample_id 还是 `(series_id,timestamp)` 冲突规则。

还需问租户基数配额、突发公平、敏感标签、数据驻留、删除时限、查询并发与成本。设备时钟可能漂移，事件时间不能直接等同于接收时间；counter 同时间戳重复和 gauge 覆盖规则也不同。后文阈值均为待校准初始策略。非目标包括按任意无界字段在线过滤、让实时面板的首次结果冒充永久最终值，以及在 WAL 未达到声明故障范围时仍返回同一种成功。

## 客观模型

状态为 `{tenant,metric,canonical_labels -> series_id}`，每个 ingest 分片保存 `{WAL, head_chunks, immutable_blocks, compaction_generation, source_epochs, shard_watermark_floor}`，保留控制面保存各数据层的删除边界。标签索引拥有 series 身份，接收复制组拥有样本耐久记录，块目录拥有查询可见 generation。令未关闭 epoch `s` 的单调进度为 `P_{j,s}`，分片 `j` 的单调最终 horizon 为 `F_j`，定义 `Advance_j(O,P,F)=F`（`O` 为空），否则 `Advance_j(O,P,F)=max(F,min_{s∈O}P_{j,s})`；普通进度更新执行 `F_j←Advance_j(Open_j,P_j,F_j)`。任何显式或 idle `CloseEpoch` 都不能把客户端自报的 terminal horizon 当作事实。服务端先令 `lower_{j,s}=max(start_floor_s,P_{j,s})`，再依据已认证的 source progress、可信接收时间及其时钟误差界、允许晚到 `L` 和可验证 terminal marker 推导 `U_{j,s}=server_verified_upper_bound`；具体 source 契约可以只使用其中能证明“低于该点不会再有普通样本”的证据，缺失所需证据时 `U_{j,s}` 不存在。客户端参数 `candidate_horizon=c` 仅是请求：服务端派生实际 `h`，或令 `h=min(c,U_{j,s})`，并且只有 `lower_{j,s}≤h≤U_{j,s}` 时才允许 close；`U_{j,s}` 无法验证、`U_{j,s}<lower_{j,s}` 或候选值低于 lower 时，epoch 保持 open，floor 不推进，进入等待或人工核验。随后在同一原子状态转换中先令 `P'_{j,s}=h`，计算 `F'_j=Advance_j(Open_j,P'_j,F_j)`，再令 `Open'_j=Open_j\{s\}`；其他 source 进度不变。这样受服务端证据约束的 `h` 在移除前参与 shard floor 推进；若 `s` 是最后一个 open epoch，移除后为空仍保留 `F'_j`，若仍有 open epoch，后续推进只由剩余进度约束。每个新 epoch 必须由服务端 `OpenEpoch` 分配 `start_floor_s≥F_j`，且 `P_{j,s}≥start_floor_s`；任何事件时间早于当前 `F_j` 的样本，即使换新 epoch 提交，也只能拒绝或进入版本化回填。相关查询分片集合为 `J` 时，`F_query=min_{j∈J}(F_j)`。接口可表示为 `OpenEpoch(source)`、`Ingest(series,timestamp,value,sample_id,source_epoch)`、`CloseEpoch(source_epoch,candidate_horizon,terminal_marker)` 与 `Query(matchers,start,end,as_of)`；`CloseEpoch` 返回服务端采用的 `h`、`U_{j,s}`、新的 `F_j` 或等待/人工状态，其证据校验、进度与 floor 推进、epoch 移除必须作为一次提交，查询返回则需说明 `F_query`、结果 generation 或是否仍可修正。

不变量是已确认样本在声明故障范围可恢复；同一样本身份重试不重复计数；同时间戳冲突按 metric 类型采用确定规则；查询不会同时读取被替换块的新旧 generation；`F_j` 对任何 source epoch 的 open/close 都单调不减，新 epoch 的服务端 `start_floor` 不低于当前最终 horizon。关闭 epoch 不得先删除 source 再凭空修改 floor：实际 `terminal_horizon=h` 必须同时满足 `max(start_floor,current_progress)≤h≤server_verified_upper_bound`，上界必须由服务端根据已认证进度、可信时间与误差界、`L` 及可验证终止标记中适用的证据推导，客户端值只能是候选。服务端无法验证上界时不得关闭或推进 floor；验证成功后才可在同一提交中先推进 source 进度、据此推进 shard floor，再移除 epoch。窗口只有在所有相关分片的 `F_query` 越过末端后才最终；事件时间早于 floor 的样本无论来自已关闭旧 epoch 还是新 epoch，都必须拒绝或进入可追踪的版本化回填，不能绕过最终性；保留删除不会因压实重新引入过期样本。canonical labels 的排序与编码必须一致，否则同一逻辑序列会被分裂。

活跃 series 数为 `S`，每条 head 元数据和缓冲为 `m` 字节，标签索引平均为 `i` 字节，内存 `M` 必须满足 `S×(m+i)≤M`。逻辑摄取率为 `λ`，有 `R` 个降采样层且每层写比例为 `a_j`，物理写近似 `λ×(1+Σa_j)`，再乘复制和压实因子。乱序率会让已封存块重开或产生补丁块，故相同 `λ` 下成本也可完全不同。

## 必然约束

[DEDUCED:time-series-database-label-cardinality-is-a-state-allocation-commitment] 标签不是普通列过滤条件。最小反例是 `http_requests_total{service="api",request_id="uuid"}` 每分钟出现一百万个 UUID；即使每条只有一个样本，仍生成一百万条 series。若每条 head 加索引约二 KiB，就立即需要约一点九 GiB 状态，尚未计 WAL 与副本。提高块压缩率不能收回接收时已经分配的身份和内存，因此基数必须在准入前估计、限制或把无界字段移到日志。

[DEDUCED:time-series-database-ingest-acknowledgement-must-bind-deduplication-and-durability] 客户端发送样本后响应丢失，它不知道首写是否进 WAL；重发可能造成 counter 重复，放弃又可能丢样。只有同一 sample_id 的幂等记录，或明确的 `(series_id,timestamp)` 冲突规则，加上可恢复 WAL 边界，才能让两次结果收敛。若 leader 只在内存中确认后崩溃，去重状态和样本一起消失，新的 leader 仍无法区分重试，因此持久性与去重不能分开声明。

[DEDUCED:time-series-database-window-finality-depends-on-a-lateness-bound] 一分钟窗口在分片 A 首次计算为九十，但相关分片 B 的 source 已安静且 floor 仍停在十一点五十九分；即使 A 已到十二点零三分，`F_query=min(F_A,F_B)` 仍未越过窗口末端，结果不能称为最终。显式终止或 idle lease 都只是 close 的触发条件，不是可任意选择 horizon 的授权；实际 `h` 必须处于 `max(start_floor,current_progress)` 与服务端验证上界之间。允许晚到 `L` 时，“约在 `end+L` 关闭”还依赖已认证 source progress、可信接收时间及误差界、事件时间约束和适用的可验证 terminal marker；客户端即使声称终止于未来，也只能请求服务端派生或截断 `h`。无法建立上界时，B 继续阻塞最终性或转人工核验，不能靠 close 抬高 floor。验证成功后才在删除 epoch 前让 `h` 参与 source 与 shard floor 的原子推进；一旦 B 的 floor 因此推进到十二点零一分，后来新开的 epoch 也必须取得不低于该 horizon 的 `start_floor`。因此早于 floor 的旧样本不能靠换 epoch 绕过关闭，只能拒绝或进入产生新 generation 的版本化回填，不能静默改写已声明最终的结果。

## 从简单方案演进

最简单正确基线是单机按 series 追加 WAL 与内存 head，周期封存按时间排序的不可变块，查询合并 head 和块。数据超过单机容量或机器故障目标后，按 tenant/series 哈希分片并复制 WAL；查询按时间与标签路由。只有长范围查询重复消耗原始样本，才增加 rollup 和多保留层；只有明确接纳晚到，才引入补丁块或独立回填路径。

第一个**待校准**切换指标是：单租户活跃 series 连续十分钟超过配额百分之八十，或某标签每小时新值超过十万，拒绝新组合并返回 cardinality 报告，将 request ID 等字段转移到日志或 trace。百分之八十与十万需按 `m+i` 实测、租户增长和扩容提前期校准；只增加机器会延后但不消除无界增长。

第二个**待校准**切换指标是：乱序样本比例超过百分之二，且 compaction backlog 预计清空时间超过三十分钟，将实时接收乱序窗口收紧为五分钟，较晚数据路由到独立修正流；若业务必须接受大规模历史回填，使用隔离分区和查询 as-of generation。百分之二、三十分钟和五分钟需要按块大小、修正价值和实时 SLO 校准。

未选择为每个样本建立全局有序日志，因为跨 series 不需要单一顺序，全球协调会把时间戳吞吐变成一个瓶颈。若业务是严格有序审计事件而非指标，日志系统会重新变优。也未选择所有查询即时扫描原始样本；若数据规模小、查询稀少且正确性优先，它更简单，只有重复聚合成本越界时 rollup 才值得其修正复杂度。

## 设计决定

本设计先规范化 metric 与标签并执行租户基数准入，再查找或创建 series_id。批次按 series 分片送到 ingest leader，使用 sample_id 或声明的同时间戳策略去重，追加到跨故障域 WAL 并达到所选确认级别后响应。source 必须先向服务端打开 epoch 并取得 `start_floor≥F_j`；普通 ingest 只接纳未关闭 epoch 且事件时间不早于当前 floor 的样本，早于 floor 的样本直接拒绝或路由到版本化回填。每个 epoch 以单调事件进度推进候选值。显式 close 与可信时钟上的 idle close 使用同一合同：服务端从已认证 progress、可信接收时间与误差界、允许晚到 `L`、事件时间约束和可验证 terminal marker 中建立 `server_verified_upper_bound`，客户端 horizon 只作候选；实际 `h` 必须不低于 `max(start_floor,current_progress)` 且不高于该上界。服务端可以把过大的候选截断到上界，但无法验证上界或截断后仍低于 lower 时，epoch 保持 open、floor 保持不变并进入等待或人工处理。只有双边界成立，才原子地以 `h` 推进 source 进度和 `F_j`、移除 epoch；即使移除的是最后一个 open epoch，推进后的 `F_j` 也继续保留。旧 epoch 关闭后不能重开，另开 epoch 也不能取得更低 floor。查询协调器对所有相关分片取 `F_query`。head 按允许乱序窗口缓冲，封存为不可变块；尚未越过 floor 的可变窗口允许普通补丁，事件时间早于最终 floor 的数据只进入隔离回填并发布新查询 generation。块目录以 generation 原子发布压实结果。

查询先按标签索引取得 series，再裁剪时间块，合并 head、基础块和当前补丁 generation。实时结果携带跨相关分片的 `F_query`；最终查询默认只覆盖 `F_query≥window_end` 的窗口，并返回结果 generation。超时重试保持批次和样本身份。过载时先停止低价值 rollup 与宽范围查询，限制新 series 和回填，再按租户公平摄取；不能丢弃已确认 WAL、把 idle source 无条件排除、为新 epoch 下调 start floor，或静默扩大去重覆盖规则。

反选方案是把所有标签和值放进通用列存储并依赖普通二级索引。它简化组件，却无法在接收前对 series 状态承诺做准入，也难以利用时间块与保留裁剪。若数据低基数、写入低且任意维度分析比实时摄取重要，分析列存会重新变优；本设计为高频指标把身份索引与时间块明确分开。

## 运行与演进

SLI 包括确认摄取延迟、WAL 未复制字节、去重命中与冲突、活跃 series、标签新值率、head 内存、乱序分布、各分片 floor 与 source 进度落后、epoch open/close 后 floor 回退检测、低于 lower 与高于服务端验证上界的候选数、缺失可验证上界而等待或转人工的 close 数、候选被截断的跨度、close 事务失败与重试数、低于 start floor 的拒绝/回填数、idle epoch 数、最终后拒绝/回填数、compaction/backfill backlog、查询扫描块数、保留删除延迟和按租户拒绝数。容量预警同时看 `S` 和增长导数；平均样本压缩率良好并不能证明 head 安全。

故障演练时间线：零毫秒客户端发送 sample_id 77；十毫秒样本达到 WAL quorum；十五毫秒响应丢失；二十毫秒 leader 崩溃；四十毫秒新 leader 从 WAL 恢复去重状态；客户端重发 77，系统返回原结果且只计一次。另一轮让相关分片 A 的 floor 到十二点零二分、B 只有一个 open idle epoch，`P_B=F_B=11:59`，验证协调器仍以十一点五十九分为 `F_query`。客户端恶意请求 `candidate_horizon=2099-01-01`；服务端依据已认证的 `P_B`、可信接收时钟及误差界、允许晚到 `L` 和该 source 的可验证 terminal marker，只能证明 `U_B=12:01`，因此将实际 `h` 截断为十二点零一分并验证 `max(start_floor_B,P_B)≤h≤U_B`。在同一 close 提交中令 `P'_B=12:01`、计算 `F'_B=max(11:59,min{12:01})=12:01`，然后移除最后一个 open epoch。若移除 marker 或无法认证时钟，`U_B` 不存在，演练必须观察到 epoch 仍 open、floor 仍为十一点五十九分并进入等待或人工核验；不能接受客户端的未来值。空集合规则保留成功 close 得到的 `F'_B=12:01`，窗口才声明最终；注入在推进与移除之间的崩溃时，恢复后只能看到整个 close 提交之前或之后，不能看到 epoch 已删而 floor 未推进的中间状态。再为同一 source 打开 epoch 时，服务端返回 `start_floor≥12:01`，分片 floor 不回退；无论客户端冒用已关闭 epoch，还是用新 epoch 提交十二点整的样本，系统都拒绝或进入回填并产生新查询 generation，而非悄悄改写最终结果。

标签 schema 迁移先让查询识别新旧规范化规则，再双索引并比较 series 映射，最后停止旧编码；回滚保留读取新 series_id。保留策略先写 delete horizon，再让 compaction 移除，备份和降采样层都需遵守。多地域复制若异步，成功语义只承诺本地域故障域；提升为地域耐久要等待远端 WAL 并接受 RTT。敏感标签要白名单化、散列或加密，避免索引和查询日志泄露身份。

## 面试考察本质

这题考察的是：给定“确认样本可恢复，且聚合最终性由跨相关分片的单调 floor 与迟到处置共同界定”的不变量，因为接收节点无法预先知道无界标签将分配多少状态、查询也不能把安静 source 或客户端自报的未来 horizon 当作已验证结束、先删 epoch 再凭空抬高 floor，或用新 epoch 重置最终 horizon，候选人能否把基数准入、去重耐久、服务端 start floor、由认证 progress、可信时钟误差界、`L` 与 terminal marker 约束的服务端验证上界、原子 close、版本化回填、保留与压实放大变成可执行取舍。

优秀回答会把 series 身份与普通字段区分，用一百万 UUID 的反例量化内存，说明 ingest ack 同时约束 WAL 和去重，并把单调分片 floor、服务端分配且不低于最终 horizon 的 epoch start floor、`lower≤h≤server_verified_upper_bound` 的通用 close 合同、无法验证上界时不推进 floor、先推进后移除的原子转换、迟到拒绝或版本化回填写进窗口契约。常见误区是只谈压缩、无条件用 `end+L` 推导最终、把 idle close 当成唯一需要可信证据的特例、接受客户端自报的极大 horizon、先移除最后一个 epoch 再无依据地提高 floor、关闭旧 epoch 后用新 epoch 回灌已最终数据、允许任意标签后再报警，或让首次 dashboard 数字无条件称为最终。

二十分钟完成 series、WAL、head、块和确认语义；四十分钟加入标签索引、乱序、单调 floor、epoch 准入、压实和保留；六十分钟再讨论回填、rollup、多租户、地域和迁移。追问应落在某个样本响应后：它在哪个故障域可恢复，重发怎样去重，所属窗口何时关闭，新 epoch 为什么不能降低最终 horizon，以及新增一个标签值究竟分配了多少长期状态。
