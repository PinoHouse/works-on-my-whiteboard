# 向量数据库

## 表面题目

设计向量数据库：客户端写入向量和元数据，以查询向量寻找距离最近的 `k` 个对象，并支持更新、删除、过滤、持久化和扩缩容。成功不仅是返回 `k` 行，而是结果属于声明的读快照与向量空间，满足可量化的近邻召回和尾延迟目标，已删除或无权对象不会从旧索引段复活。

题名容易把系统简化成“一个 ANN 库外加分片”。真正困难是近似搜索主动放弃全量比较、元数据过滤改变候选概率、不可变 segment 与后台 compaction 延迟可见性，以及模型升级可能让向量空间不再可比。本题负责在线向量存储和查询，不负责上游文档如何切块、何时重嵌入，也不把相似度等同于业务相关性。

## 反问与边界

先问向量条数 `N`、维度 `d`、数据类型、写删速率、查询峰值、租户与热点分布；距离是余弦、内积还是欧氏，是否先归一化。质量要定义精确基线下的 `recall@k`、过滤后返回完整率与业务相关性，体验定义读己之写、写可见 lag、查询 `p95/p99`，成本定义每百万向量 RAM/SSD、每千查询 CPU 与建图放大。

过滤是租户隔离、ACL、时间范围还是任意谓词，选择性分布决定先过滤还是后过滤。删除要求立即不可见还是允许秒级窗口，故障恢复能否挂载旧快照；写确认代表 WAL 持久、memtable 可查还是所有副本索引完成。备份要分别问 RPO（允许丢多少新写）与 retention/最老可恢复时间点（多老的快照仍能恢复），二者不能混用。还要问索引类型、分片/副本、跨地域、模型/归一化版本和数据删除法规。

## 客观模型

接口为 `Upsert(id, vector, metadata, vector_space, idempotency_key)`、`Delete(id, delete_epoch)`、`Search(query, k, filter, read_generation, quality_budget)` 与 `Get(id)`。权威写日志为每条变更分配单调 sequence；memtable 与不可变 segment 都记录 `(id, version, vector_space, metadata, seq)`，墓碑记录 `(id, delete_seq)`。segment manifest 保存 generation、segment digest、图参数、覆盖 seq 范围和可见 tombstone watermark；查询一次绑定完整 manifest。

精确扫描工作约为 `O(N*d)` 次数值操作；ANN 将访问候选数压到 `V≪N`，代价是可能漏掉真近邻。原始向量容量约 `N*d*b`，图边、元数据、过滤索引和副本形成放大 `A`，总量约 `N*d*b*A`。若先取 `Kc` 候选再做选择率 `s` 的过滤，期望留下 `Kc*s`；当 `k=10, Kc=100, s=0.01` 时平均只有 1 个，说明后过滤无法仅靠固定候选保证完整结果。

状态所有者是 WAL/版本表的对象真值、manifest 服务的可服务 segment 集和权限元数据的当前 epoch。关键不变量：查询向量只与相同兼容 vector_space 比较；同一 id 只返回读快照内最高版本；delete_seq 覆盖的旧版本不能由任何可服务 generation 返回；过滤条件在结果发布前强制执行。确定的 segment 构建可复现索引字节，却不保证浮点 tie、并行遍历或上游概率模型输出逐次相同。

## 必然约束

[DEDUCED:vector-database-ann-trades-recall-for-bounded-work] 近似最近邻以不遍历全部向量换取有界工作量，因此延迟下降的同时必须显式接受并度量召回损失。若一百万个向量中真正第 10 近的节点不在已访问的 2,000 个图节点内，算法无法知道它存在；增加 `efSearch` 可提高访问概率，却增加 CPU、缓存未命中与 p99。没有针对过滤和数据分布的精确扫描对照，所谓“快”无法说明丢了多少结果。

[DEDUCED:vector-database-delete-requires-generation-fenced-tombstones] 删除只有在墓碑覆盖所有可服务索引代际并阻止旧段重新挂载时才成立，移除一份可变索引记录不足以防止复活。事件序列：segment S1 含 id=7，今天删除后当前段已压缩，但一份保留六个月的月龄备份仍含 S1；“RPO 一小时”只限制新写损失，不会让该老备份消失。墓碑必须覆盖最老可恢复 snapshot/backup 的完整 retention，或恢复流程必须先回放不可截断删除日志，并以 `minimum_safe_seq/generation` 拒绝任何无法补齐删除的恢复点。

[DEDUCED:vector-database-filtering-must-enter-candidate-planning] 高选择性元数据过滤若只在近邻搜索后执行，会耗尽候选预算并返回不足 k 个结果，过滤必须进入候选规划。上述选择率百分之一的反例显示取 100 只剩约 1 个；盲目把候选放大到 1,000 又使尾延迟十倍。系统需按过滤基数选择预过滤 bitmap、分区路由、联合遍历或自适应补搜，并把返回不足作为质量指标而非悄悄缩短结果。

## 从简单方案演进

基线是单机持久日志加精确扫描，更新覆盖版本、删除写墓碑。它对小集合给出精确真值，也为 ANN 评测提供基准。当查询 CPU `p95` 超过延迟预算的百分之五十，或 `N*d` 扫描使峰值核时超过预算，才建立不可变 ANN segment；它降低查询工作，却引入 build lag、召回误差和 segment 合并。

第二步把热 memtable 精确扫与冷 segment ANN 合并，查询按版本去重。若 segment 数超过 32 且查询扇出使 `p99` 增长百分之二十，或墓碑比例超过百分之十五，触发 compaction；这些是待压测和删除演练校准的起点，降低阈值会增加写放大，升高会增加查询放大与复活风险。过滤后 `recall@10` 比无过滤低超过三个百分点时，引入选择性估计和预过滤路径，而不是只调大图搜索。

第三步按租户/向量空间和容量分片，副本提供读吞吐与恢复。若单 shard 构建时间超过 RTO 一半或热点 shard 的 CPU 是中位数两倍，重分片或复制热点；迁移通过 manifest generation 双读校验后原子切换。阈值需由数据偏斜、业务 SLO 和成本压测决定。

未选择一开始全部内存 HNSW，因为写放大与 RAM 成本高、删除清理复杂；数据集可完整放内存且更新稀少时它重新变优。也未选择纯后过滤，因为高选择性 ACL 会耗尽候选；过滤字段固定且分区均匀时，物理分区索引可能比通用联合搜索更简单。

## 设计决定

写先进入复制 WAL 并获得 sequence，当前 memtable 立即按最高版本可查；后台按 vector_space 构建不可变 segment。manifest 发布是唯一让新 segment 可服务的线性化点，一次查询固定 generation，扇出各 shard 后按距离、版本和 id 稳定合并。元数据统计估计过滤选择性：高选择性走 bitmap/分区预过滤，中等选择性联合遍历，低选择性可后过滤并自适应补搜。

删除写入高于旧版本的 tombstone，查询合并阶段和 segment 内遍历都检查；compaction 只有在最老可恢复 snapshot、所有仍在 retention 的备份及副本 watermark 都越过 delete_seq 后才丢墓碑。若成本要求更早清理，则删除日志必须在备份完整 retention 内不可截断，restore 先回放到 `minimum_safe_seq`，无法回放的老备份明确不可恢复。旧节点/备份都必须通过 minimum_safe_generation 门禁，不能自行宣布可服务。

过载先降低非关键查询的 `efSearch` 并标记质量等级，再限流大 `k` 和昂贵过滤，绝不绕过 ACL/墓碑。索引 generation、构建器和浮点配置全版本化以支持归因，但 ANN 并行遍历的 tie 与上游 embedding 可变性仍意味着不能把结果逐字节确定当默认契约。

## 运行与演进

SLI 联合观察 `recall@k`、过滤后完整率、重复/旧版本率、删除后残留，及搜索 p50/p99、写可见 lag、segment build lag、compaction debt。成本看每百万向量 RAM/SSD、每千查询距离计算、写放大和副本传输；按 shard、filter 选择率、vector_space、generation 分桶，避免平均 recall 掩盖高选择性租户失败。

故障演练：t0 S1 含 id 7/v3，并生成一份需保留六个月的月备份；t1 delete seq=900 持久化；t2 当前段已 compaction；t3 从月龄备份恢复。恢复器看到 backup seq 低于 `minimum_safe_seq=900`，必须先回放保留期内不可截断的删除日志再允许服务；若日志缺口存在则拒绝该恢复点。演练刻意把 RPO 设为一小时，验证它不影响六个月 retention，id 7 仍不复活。

每个 release 绑定不可变 eval manifest：查询向量/过滤条件集 digest、精确 top-k 与可见性标签 digest、相关性人工 rubric 或 judge version、recall/过滤完整率/p99/成本指标实现版本。新 generation 只有在同一 manifest 上与精确扫描比较才可声称提升；标签或指标改版先重建基线。灰度查询与回滚目录都记录该 manifest。recall 低于业务下限立即回滚，compaction 超预算时可延长 segment，但不可越过删除安全水位。

## 面试考察本质

给定“读快照中的最高版本可查、确认删除不可被任何旧 segment 复活”这一存储不变量，因为精确距离对大 `N*d` 成本过高、过滤选择性事前不完全已知、派生索引异步构建，候选人应推导出带版本的 WAL/segment/manifest、可测 ANN 误差、过滤感知规划和墓碑安全水位，并依据召回、p99 与存储/构建成本选择。

优秀信号是先建立精确基线，再谈 ANN；区分向量空间与索引 generation；用 `Kc*s` 解释过滤；把删除、恢复与 compaction 放在同一时间线上。常见误区是只说 HNSW、以返回数量代替 recall、删除只改缓存、按 id 哈希后忽略热点或让一次查询混读新旧 manifest。

二十分钟回答接口、容量公式、精确扫描到 ANN 的切换；四十分钟加入 segment、过滤、墓碑与分片；六十分钟讨论构建灰度、恢复、租户隔离和成本。追问用“100 候选、1% 过滤”检验规划，用“删除后挂旧快照”检验安全水位，用“模型换维度/归一化”检验兼容空间。它考的是有界工作量下可声明的近似存储，不是会背某个向量索引名字。
