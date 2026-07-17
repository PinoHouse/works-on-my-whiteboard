# 嵌入与索引流水线

## 表面题目

设计一条把源数据变成可查询向量索引的流水线。源文档新增、修改或删除后，系统抽取确定 revision，清洗、切块、调用 embedding 模型、写入索引并发布给查询端；模型或切块策略升级时，还要安全重建全量语料。成功不是“任务跑完”，而是每个可服务索引代都能说明来自哪些源 revision、采用哪套处理和向量空间，查询不会把不兼容的新 query vector 发到旧文档索引。

题名容易被误写成通用消息队列 ETL。本题独有的难点是嵌入模型改变坐标空间、切块改变对象身份、全量回填与持续增量并发、以及派生索引可以重建却不能随意混代。它不负责 ANN 内部查询算法，也不负责最终生成模型如何回答。

## 反问与边界

先问源系统能否给单调 revision 或一致快照，更新/删除峰值、语料字节量、文档长尾、语言和租户隔离。新鲜度 SLO 是分钟还是天，模型升级允许多久双代，失败重试能否重复调用计费 API。质量需定义离线检索 `recall@k`、chunk 覆盖/边界准确、下游任务成功率与删除完整性；体验看 source-to-searchable lag，成本看每百万 token 的嵌入费用、全量读写字节与双代存储。

兼容性要明确模型 digest、维度、归一化、池化、tokenizer、预处理和距离函数，不能只写模型名称。删除需传播到哪个 generation、备份和缓存；源权限变化是否需要重算向量还是只改元数据。多地域由一地构建复制，还是各地独立推理；浮点硬件差异能否接受。非目标是保证相同文本跨模型向量相同，也不把流水线作业确定性误认为下游概率回答确定。

## 客观模型

接口包括 `Ingest(source_id, source_revision, operation)`、`StartBuild(config_digest, source_snapshot, build_epoch)`、`Publish(generation, build_epoch, expected_previous)` 与 `GetLineage(object_id, generation)`。处理谱系为 `source_revision → extractor_version → normalizer_version → chunker_version → tokenizer_version → embedding_model_digest/execution_profile → vector_space → index_generation`。chunk identity 由 source id、revision、稳定区间与 chunker version 导出，不能在切块策略变化后假装是同一对象；每个 task attempt 另带只增不减的 `task_attempt_epoch`。

设语料总 token 为 `T`，平均重叠比例 `r`，实际嵌入 token 约 `T/(1-r)`；每批 `B` token、模型吞吐 `q` token/s 时，理想回填时间下界约 `T/((1-r)*q*workers)`，还未计源读取、限流和失败。一次模型升级的字节/计算放大近似为全语料读取一次、嵌入推理一次、索引写一次，再乘副本与双代系数。少数超长文档、压缩包和高频更新对象会支配队列长尾。

源系统拥有 revision 真值，任务账本拥有每个 `(source_revision, config_digest)` 的幂等状态和当前 attempt epoch，build coordinator 拥有当前 build_epoch，generation manifest 拥有查询路由的完整分片集合。关键不变量：query encoder 与索引文档向量的 compatibility key 必须一致；只有当前 task/build epoch 能提交 vector、segment、manifest 和完成状态；generation 只能在所有必需分片、watermark 和墓碑闭合后发布；删除不得被迟到旧 upsert 覆盖。版本化执行可以解释字节差异，但 GPU 并行浮点和下游生成仍可能非确定。

## 必然约束

[DEDUCED:embedding-index-pipeline-compatibility-requires-joint-versioning] 查询向量与文档向量只有在兼容的切块、预处理和嵌入空间中才可比较，单独记录模型名称不足以证明兼容。反例是同一模型权重下，旧流水线先 L2 归一化并用内积，新查询未归一化却仍标“model-v3”；距离排序已改变。更明显的是 tokenizer 或 pooling 改变维度/语义。故 compatibility key 至少覆盖模型摘要、tokenizer、预处理、归一化、距离与必要执行配置，网关不匹配时拒绝而不是静默搜索。

[DEDUCED:embedding-index-pipeline-cutover-requires-generation-manifest] 查询路由必须原子选择一个完整索引代际，逐分片切换会让同一次请求混合新旧空间并产生无法解释的结果。事件序列：分片 A 已切 model-v2，B 仍 v1；查询只编码成 v2，却同时扇出 A、B。B 返回的分数与 A 不可比较，合并 top-k 没有意义。即使两个模型维度相同也不能假定空间兼容，因此发布必须等完整 generation READY，再由目录 CAS 切换，单请求固定该代。

[DEDUCED:embedding-index-pipeline-rebuild-amplification-must-be-budgeted] 模型或切块变更会把一次逻辑升级放大为全语料读取、推理、写索引与双代验证，回填必须受成本和新鲜度预算约束。一亿个平均 500 token 的块就是 500 亿 token；若每天又有百分之一变更，回填期间忽略增量会落后百万对象。系统需要 snapshot+change-log 水位、限额和优先级，否则全量作业既挤压新数据又可能永远追不上。

## 从简单方案演进

最简单基线是单进程按源快照顺序读取，确定性切块、同步嵌入后写一个离线索引，完成后整体替换。它适合小静态语料且容易审计。当一次构建时间超过新鲜度 SLO 的一半，或失败重跑费用超过日预算的百分之十，先加入持久任务账本、内容摘要去重和分批 checkpoint。阈值是待容量压测与账单校准的初始值。

第二步处理持续变更：在 snapshot revision `R0` 全量回填，同时记录 `R0` 之后的 change log；全量完成后按 revision 顺序追平到 watermark，再发布。若追平期间 backlog 增长率连续十五分钟大于消费率，暂停低优先全量、扩 worker 或按租户限流；若 source-to-searchable `p99` 超过目标，则给增量保留容量。十五分钟和容量比例需故障演练校准，越短越容易因瞬时峰值抖动。

第三步模型升级采用 G 与 G+1 双代构建、shadow query 与整体 cutover。只有固定评测上 recall/任务质量不低于门槛、p99 与单位查询成本在预算内、删除水位追平，才切换。若双代存储超过配额百分之八十，宁可减慢实验或淘汰失败候选，不可提前混代发布。

未选择在线请求遇到旧对象就懒重嵌入，因为首访问尾延迟不可控且热门对象先升级造成偏置；当语料巨大、访问极稀疏、允许结果逐步改善时懒迁移重新变优。也未选择对模型名相同就原地覆盖，除非供应方能提供不可变 digest 和明确兼容证明。

## 设计决定

接入层按 source revision 读取不可变内容并把 upsert/delete 写入任务账本。任务逻辑键为 `(source_id, source_revision, config_digest)`，每次领取租约都推进 `task_attempt_epoch`；worker 生成带摘要的 chunk 和向量，但所有 vector/segment 写、完成标记都必须同时匹配当前 task epoch 与当前 build_epoch。租约过期、取消或 coordinator 换主会推进 epoch，旧 attempt 即使 source revision/config 与新任务相同也不能再提交。相同 epoch 内重试比较 digest 并幂等，删除成为同序列的墓碑任务。

每个 build 从固定 source snapshot 开始，manifest 列出 build_epoch、配置 digest、compatibility key、所有 shard digest、source/change-log watermark 和删除水位。segment manifest、最终 manifest 与 READY completion 都按当前 build_epoch 条件提交；换主后旧 epoch 的“最后一个分片完成”不能提前封口。验证器确认分片完整、lineage 可回源、query encoder 已部署且质量/延迟门槛通过，目录才以 expected_previous CAS 发布。查询整次绑定 generation；切换失败保留旧代。

过载先暂停实验性全量 build，再降低低优先租户回填并保护删除和增量；绝不跳过 lineage 或兼容检查。模型、数据、chunker、tokenizer、index builder 和 runtime 都记录版本。即使这些输入固定，GPU 浮点归约可能产生微小向量差异，下游模型输出更是概率性的，因此保证是可归因与质量边界，不是逐位复现。

## 运行与演进

SLI 包括 source-to-searchable lag、各阶段 backlog age、任务重试/重复计费、generation 完整率、删除水位、lineage 断链；质量看与精确/标注集比较的 recall、chunk 覆盖和下游成功率；成本看每成功对象的源读取字节、嵌入 token、索引写放大与双代存储。所有指标按 config、generation、租户和文档长度分桶。

故障时间线：t0 build G2/epoch 7 固定快照 R100；t1 worker W 租约过期，换主把 build 推到 epoch 8 并重派同一 source/config；t2 W 迟到上传 vector、segment 和 completion，全部因 epoch 7 被拒绝；t3 新 worker 在 epoch 8 处理 R101 删除并把墓碑水位追到 R105；t4 只有 epoch 8 manifest 可 READY。随后模拟目录切换中断，CAS 要么仍指 G1，要么完整指 G2，不出现混代分片。

每个 release 绑定不可变 eval manifest：查询集与源快照 digest、相关文档/chunk 边界标签 digest、人工任务 rubric 或 judge version、recall/chunk-coverage/新鲜度指标实现版本。影子 generation 只有在同一 manifest 上比较 recall、下游成功、延迟和成本才可声称优劣；数据或 judge 改版先重建基线。灰度与回滚均记录该 manifest。定期演练旧 epoch 双写、删除与目录故障；质量下降或单位嵌入成本越界则取消 build，增量 lag 接近 SLO 的百分之八十则抢占全量容量。

## 面试考察本质

给定“查询只能比较兼容向量空间，且一个可服务代必须对应闭合的源 revision 与删除水位”这一不变量，因为嵌入是昂贵派生数据、全量回填与持续变更并发、模型/切块升级会改变对象和空间，候选人应推导出联合 compatibility key、snapshot+log 追平、完整 generation manifest 与双代 cutover，并在新鲜度、质量、重建成本和双代容量间取舍。

优秀信号是把源 revision 到 index generation 的谱系画完整，解释为什么同维度不等于可比较，量化全量 token 放大，并让删除优先于迟到回填。常见误区是按模型名称判断兼容、逐 shard 直接替换、全量重建时丢增量、或把可重跑作业说成下游回答确定。

二十分钟回答兼容键、任务幂等与容量公式；四十分钟加入 snapshot+change log、墓碑和 generation CAS；六十分钟讨论 shadow 评测、多租户配额、回滚、跨地域复制与成本治理。追问用“同模型不同归一化”检验兼容，用“回填时对象被删除”检验版本条件，用“一半分片已升级”检验发布原子性。本题考的是派生数据代际迁移，而不是再设计一个推理网关。
