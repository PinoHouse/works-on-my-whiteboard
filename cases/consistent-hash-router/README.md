# 一致性哈希路由器

## 表面题目

设计一致性哈希路由器，表面动作是把 `key` 计算到某个节点。真正的状态变化发生在成员加入、退出或权重调整时：控制面发布新的 ring generation，数据需要从旧 owner 迁到新 owner，写权限也必须沿同一边界交接。查找函数本身可以是本地且确定的，但“所有路由器用的是不是同一份成员快照”决定同一可写 key 会不会同时落到两个 owner。

题名容易把最小迁移、负载均衡和服务健康混成一个承诺。一致性哈希只限制成员集合变化时重新映射的键比例，不保证键大小、请求频率或租户价值均匀；虚拟节点只能降低随机区间偏斜，不能拆开单个热 key。本设计面向缓存或分片数据的持久键归属，强调 generation 与迁移协议，不把它写成请求级鉴权网关或 DNS 新鲜度问题。

## 反问与边界

先问 key 数 `K`、键和值大小分布、请求频率偏斜、成员数 `N`、扩缩容频率、复制因子、可接受迁移窗口和缓存冷失效率。还要确认路由错误只是缓存未命中，还是会把写入送到错误的持久 owner；前者可容忍短时双读，后者必须 fence 旧 owner。key 规范化规则、租户命名空间和哈希算法版本也属于协议，不能由各语言客户端自行猜测。

故障边界包括成员瞬时失联是否立即移环、控制面分区时谁能发布新 generation、迁移中读写怎样处理、旧路由器最长存活多久。多地域场景要问 ring 是区域内还是全球，跨区带宽是否进入迁移预算。安全上，恶意 key 可制造哈希碰撞或热点，成员更新必须鉴权签名。非目标包括靠哈希证明数据已复制、让所有 key 绝对均匀，以及用本地健康探针单方面改变全局写归属。

## 客观模型

核心状态为 `(ring_generation, hash_version, virtual_nodes, member_weights, key -> owner_set, migration_phase, ownership_token, committed_tail)`。控制面拥有单调 ring generation 和成员权重的唯一发布权；路由器以完整 ring 快照做本地查找；数据分片的复制日志拥有 key 的实际版本、已提交尾部和单调 ownership token。generation 只回答“路由器会把请求送到哪里”，ownership token 才在实际写仲裁点回答“这个 owner 是否还能提交”。正常查找将规范化 key 哈希到环上顺时针第一个 vnode，并按复制规则得到 owner set。迁移协调器只为新旧映射差集建立任务和写权限交接，不扫描不受影响的 key。

不变量包括：同一 generation、同一规范 key 在所有路由器映射到同一 owner set；新 owner 开放持久写前，旧 owner 的数据面必须已经持久提交更高 token 的 fence；旧 token 的请求在每个写 quorum 成员投票或落盘前都要被校验并拒绝，不能只在路由器或控制面拒绝；成员变化后只有映射差集中的 key 允许迁移；迁移完成前旧副本不能被当成新鲜权威。旧复制配置在交接期间冻结，fence quorum 与任意旧写 quorum 相交，故 fence 后旧 token 不可能再取得提交 quorum。若从 `N` 个等权成员扩到 `N+1`，理想一致性哈希期望迁移比例约为 `1/(N+1)`，总键数为 `K` 时约迁移 `K/(N+1)` 个；实际带宽必须计算 `B_move = Σ size(key)`，不能只数键。

对比 `hash(key) mod N`，分母变化会让大多数余数改变。虚拟节点数 `V` 增大通常降低成员区间长度方差，但 ring 描述、排序、发布和差异计算近似随 `N×V` 增长。一个占总流量 30% 的热 key 即使落在统计均匀的环上，也会独占其 owner；哈希均匀不等于负载均匀。

## 必然约束

[DEDUCED:consistent-hash-router-generation-skew-can-create-dual-writers] 一致性哈希的确定性前提是参与者使用同一成员快照。generation 41 把 `invoice-9` 映射到 A；候选 generation 42 把它映射到 B。路由器 X 未更新仍把写发 A，路由器 Y 已更新把另一写发 B，两套 ring 各自计算正确。即使控制面已经生成 migration token 并声称 A 被 fence，只要 A 与控制面隔离、旧路由器仍可达 A，且 A 的实际写 quorum 尚未持久看到 fence，A 就仍可能提交。正确交接必须先让冻结的旧 owner/quorum 在数据日志中提交更高 ownership token 的 `FENCE`，取得包含已提交尾部的持久 quorum 证明；B 追平该尾部并在自身 quorum 激活新 token 后，才开放 generation 42 写。拿不到证明就停在旧 owner 或暂停该范围，不能凭控制面发布推断撤权。

[DEDUCED:consistent-hash-router-minimizes-membership-movement-not-load-skew] 从十个等权成员加到十一个，期望只移动约十一分之一键，这描述的是映射变更数量。若被移动的一个 key 有一太字节，而其余千万键各一千字节，迁移字节和窗口仍由大 key 主导；若一个不迁移热 key 承担三成请求，新成员也不会自动分走它。故一致性哈希减少成员变更扰动，但不提供容量保证，热点需要显式拆分、复制读或独立路由。

[DEDUCED:consistent-hash-router-vnode-count-trades-variance-for-control-plane-churn] 增加 vnode 让每个物理成员获得更多随机小区间，通常能平均区间长度；同时 ring 条目、变更差集、签名快照和每个路由器内存都线性增加。最小例子是十个成员从每成员十个 vnode 增到一万个，条目从一百变十万，即使业务 key 数不变，发布和比较也扩大千倍。因此 vnode 数是偏斜与控制面成本的取舍，不是越多越安全。

## 从简单方案演进

最简单正确基线是固定 `hash(key) mod N`，成员集合很久不变时查找便宜且清晰。第一次扩容前预测迁移：若一次改变 `N` 使迁移比例超过 `20%`，或缓存预热与复制字节超过迁移窗口带宽预算的 `60%`，切换到带 generation 的一致性哈希并只预热映射差集。它减少扰动，却新增 vnode、ring 发布、generation 偏斜与迁移状态机。

第二步为物理成员配置多个 vnode 和权重，根据容量做受控再平衡。第二个待校准切换指标是最大 owner 请求率连续十分钟超过集群中位数的 `1.5` 倍，或任一 owner 存储使用率超过 `75%`：先区分热 key、键大小和区间偏斜，再选择加权 vnode、热点隔离或拆 key。`20%/60%` 与 `1.5 倍/75%` 是两组待压测和迁移演练校准值；调高会少触发搬迁但增加冷失效和容量风险，调低会更早再平衡并提高控制面 churn 与迁移带宽。

持久写场景再引入迁移阶段。`PREPARE` 时 B 复制 A 的快照与增量，但 A 仍是唯一写 owner，B 不因拿到候选 generation 或 token 而接收客户端写。`CUTOVER` 先让冻结配置中的 A 写 quorum 把 `FENCE(new_token)` 排在所有旧写之后持久提交，并返回绑定 key 范围、旧配置、new token 与最终 committed tail 的 quorum certificate；单副本 owner 则必须由 A 本机持久 ACK，控制面超时不能替代。随后 B 追平并校验到该 tail，目标 quorum 验证 certificate 后持久 `ACTIVATE(new_token, tail)`，此后才发布并开放新 generation 写。若 fence 尚未发出或明确未提交，迁移停在 `PREPARE`、A 保持写权；若请求已发但 ACK 丢失，B 保持关闭，A 若未提交 fence 仍是唯一可能写 owner、若已提交则该范围暂时不可写，查询旧 quorum 后只能按“未提交则恢复 A、已提交则继续 B”之一收敛，绝不能猜测。`CLEANUP` 只在路由器越过旧 generation、旧 token 拒绝可观测且保留窗口结束后回收旧副本。缓存场景可以快速切环并在新 owner 缺失时回源重填，不必等待这套持久写交接；两者不能共用“丢了再算”的故障语义。

未选择全量中心目录 `key -> owner` 作为默认路由，因为它让每次查找或大规模目录缓存成为新瓶颈。当 key 数很小、每个 key 的位置经常因容量和合规单独调整，或者需要精确热点放置时，显式目录会重新变优。也未选择发现成员变化后由每个路由器独立移环，因其无法给出统一 generation 和写权交接。

## 设计决定

本设计由单一逻辑控制面发布签名的不可变 ring 快照，包含 generation、哈希版本、规范化规则、vnode、权重与已激活的 ownership token。路由器只有在完整校验快照后原子切换；每个持久写携带 generation 和 token，但 generation 不能授权写。旧数据副本在参与写 quorum 前，以自身持久复制日志中的当前 token 校验请求；低 token 或 owner 不匹配即拒绝并返回重路由信息，因此旧路由器即使仍能连接 A，也无法绕过数据面的 fence。目标 quorum 只有验证旧 quorum certificate、追平其 tail 后才能激活对应 token，不能接受控制面单独下发的“已切换”标志。读在迁移期按阶段选择旧、新或双读，但版本比较规则固定，不能以“谁先响应”裁决新鲜度。

成员故障不会由一个路由器本地永久改环。控制面达到故障判定后只能提出下一 generation；持久数据先复制，旧 quorum 再提交 fence，目标追平 fence tail 并激活新 token，最后才开放新 generation 写。fence certificate 是切写前置条件而非控制面状态的派生字段；旧 owner/quorum 不可达时，系统牺牲该范围的迁移可用性，也不让 B 先写。缓存型数据可快速切环并接受冷 miss，热点 key 则复制只读副本或加盐拆分，但持久写聚合仍有明确 owner。控制面不可用时继续使用最后完整且已激活的 generation，宁可暂停成员变化，也不让不同数据面自创成员集。

反选方案是 rendezvous hashing，它无需维护有序 ring，按 key 对所有成员打分，成员数不大时实现与加权更直接；但每次查找朴素成本随成员数增长。本设计已有大量数据面客户端和 ring 快照，因此选择 vnode 环。当成员少、权重频繁变化且可接受 `O(N)` 或有候选优化时，rendezvous 会重新变优。

## 运行与演进

核心 SLI 是各 generation 路由器占比、旧 token 在数据 quorum 的拒绝数、fence certificate 等待时间与未知态数量、目标追平 fence tail 的 lag、预测与实际迁移键数/字节、迁移剩余时间、缓存冷失效率、最大/中位 owner 请求与存储比、热点 key 占比和 ring 发布 `p99`。过载时先暂停低优先再平衡与额外副本，限制迁移带宽，再隔离热点；不能为了加快扩容跳过数据面 fence 或把控制面 generation 当作撤权证明。

故障演练时间线：0 秒，generation 41 令 `invoice-9` 归 A，数据面 token 为 9001；5 秒，控制面提出 generation 42、B 预复制完成，但 42 尚不可写；6 秒，A 的冻结写 quorum 在日志位置 L 提交 `FENCE(9002)` 并返回 certificate，此后任意 token 9001 写 quorum 都因相交副本拒绝而无法提交；7 秒，B 追平 L、目标 quorum 持久 `ACTIVATE(9002,L)`，控制面此时才发布 42，路由器 Y 切换并由 B 接受写；8 秒，仍停在 41 的 X 把 token 9001 写发给仍可达的 A，请求在 A 的实际写仲裁点被拒绝。若 6 秒拿不到旧 quorum 的持久证明，B 在 7 秒不得开放；若绕过该条件，A、B 各接受一个版本即可复现双写。X 恢复后原子加载 42 并重试原幂等写，不能把 A 的旧结果覆盖 B。

哈希算法升级先双算旧新映射并估计差集字节，再灰度只读流量，最后按迁移状态切写；回滚也必须发布更高 generation，不能倒回编号。权重调整限制每轮最大迁移量，避免多个成员同时预热拖垮源。租户 key 在哈希前规范化并加入命名空间，敏感 key 不进入路由日志，成员快照由可信控制面签名。

## 面试考察本质

这题考察的是：给定“同一持久 key 的新 owner 只有在旧 owner/quorum 已持久撤销写权后才能开放，成员变化只迁移必要差集”的不变量，因为路由器本地计算和控制面 generation 都无法证明隔离的旧 owner 已在实际提交路径失去权限，候选人能否从 quorum 相交推导出数据面 fence certificate、目标追平与后开写顺序，再用一致性哈希和热点隔离换取较小迁移量，同时承认它不保证负载均匀。

优秀回答会区分键数迁移和字节迁移，写出 `K/(N+1)` 的期望与 `Σ size(key)` 的真实带宽，用 generation 41/42 的 `invoice-9` 双写击穿“哈希确定所以安全”，并明确旧 quorum 持久 fence、目标追平 fence tail、目标激活与新环发布的先后关系。常见误区是每个路由器独立摘成员、把更多 vnode 当热 key 解法、把控制面 token 当成 A 已撤权的事实，或在迁移期让新旧 owner 都能写。

二十分钟回答应完成 ring、owner 不变量和模 N 基线；四十分钟加入 vnode、迁移比例、generation 与切写；六十分钟再讨论复制、热点拆分、算法升级和多地域。追问应固定在 X、Y 使用 generation 41/42 的八秒窗口，要求候选人逐事件指出写权限、数据新鲜度和恢复路径，从而区别“会画哈希环”和“会设计归属迁移”。
