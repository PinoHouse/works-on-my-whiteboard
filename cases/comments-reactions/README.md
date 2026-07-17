# 评论与互动

## 表面题目

设计评论与互动，表面流程是用户发布评论、回复、点赞或取消点赞，页面展示回复树、数量和热评。真正的状态变化是线程内评论追加、审核版本推进，以及每个 actor 对 target 的反应关系在 active/inactive 间条件转换；点赞数、热度和展开摘要都是派生读模型。成功必须区分“我的按钮状态已确认”和“公开计数已追平”。

题名掩盖了局部顺序与全局热度的差别。`created_seq` 只在一个 thread 内给权威顺序，跨帖子热评与点赞 rank 可重算。本设计不把首页 feed、私信或通知恰好送达并入评论状态所有权。

## 反问与边界

先问评论是否可编辑、删除后留墓碑还是整棵隐藏、最大深度和分页语义；审核决定多久必须生效，被删 parent 的回复如何展示。反应是一种还是多种，同一 actor 能否同时选择多个，按钮写入要读己之写还是仅最终一致，公开数允许多大误差和延迟。热点必须按单 target 峰值、嵌套回复和通知放大规划。

还需确认匿名互动、机器人限制、租户隔离、未成年人和隐私删除。正文与 @用户名是敏感内容，缓存键必须包含审核版本，日志不复制正文。非目标是用计数证明某用户已点赞、让热评 rank 修改线程顺序，以及为了即时计数牺牲用户关系唯一性。

## 客观模型

最小命令为 `AddComment(thread,parent,body,idempotency_key)`、`EditComment(comment,expected_body_version)`、`Moderate(comment,expected_version)`、`SetReaction(actor,target,type,desired_state,expected_version)` 与 `GetThread(cursor)`. 线程 owner 拥有 `created_seq` 和 parent 约束；审核库拥有可见性版本；关系表以 `(actor,target,type)` 为唯一键拥有用户状态；计数器和热榜只拥有派生聚合。

不变量是 active 关系唯一；评论只能挂在存在且允许回复的 parent；迟到编辑、回复入口或缓存填充不得越过删除/封禁版本；线程内 sequence 单调，热评 rank 不改 parent-child。若把 target 的计数拆 `S` 个分片，写热点期望从 `λ` 降到 `λ/S`，读需合并 `count=Σ counter_i`，而纠偏真值仍是 active membership 集合大小。

回复树读取成本随深度和展开节点数增长，不等于总评论数。通知、计数、摘要在写后异步物化，各自失败不回滚已接受评论；审核则是展示前硬过滤。

## 必然约束

[DEDUCED:comments-reactions-membership-is-authoritative-and-counter-is-derived] 某用户是否点赞需要唯一关系状态，计数器只保留总量，不能反推出 actor。最小反例是 U 在地域 A、B 同时 Like，两边各先 `+1`，关系表唯一键只保留一条 ACTIVE；随后 Unlike 减一后公开数仍为 1，实际 active 为 0。应先条件转换 membership，再按 outbox 更新分片计数并周期对账。匿名不可撤销浏览量只需近似时，纯计数器可以成立，但不适用于可取消点赞。

[DEDUCED:comments-reactions-thread-order-is-local-to-a-thread-not-a-global-social-order] thread owner 能为同一线程追加 `created_seq`，却没有跨所有帖子协调的必要信息。反例是线程甲 seq 9 与线程乙 seq 3 在两个区域提交，网络延迟使到达顺序相反；热度模型又把更旧评论排前。把两个 seq 比大小没有语义。分页可绑定一个 thread 的序列快照，跨帖发现与热评只能声明为派生 rank，不能用于审计谁先发表。

[DEDUCED:comments-reactions-moderation-version-must-fence-late-edits-and-cache-fills] 异步工作者可能只掌握审核前正文。事件序列是 0ms 评论 C 在审核 v4，10ms 版主删除并提交 v5，20ms 旧缓存填充携带 v4 写正文，30ms 用户发回复。若缓存和回复入口不校验 v5，删除内容重新可见且继续长出子树。读取先看墓碑 v5，迟到 v4 条件写拒绝，回复返回 parent unavailable；计数可稍后纠偏但不能恢复正文。

## 从简单方案演进

最简单正确基线是一张评论表和唯一 reaction 表：线程内条件追加，点赞事务性切换关系并同步重算小目标计数。低流量足够清楚；病毒 target 使关系行和单计数键争用时，将计数改为 outbox 异步聚合，用户按钮仍读自己的 membership。新增计数 lag、重复消息和对账。

第一个待压测校准指标是某 target 的 reaction 条件写冲突超过 `1%`，或单键计数写 `p99` 超过 `50 ms`：启用分片累加器并异步校验 membership。第二个待读取回放校准指标是线程深度超过 `8` 层且展开 `p99` 超过 `200 ms`：折叠深层回复、按 parent 分页并预物化摘要。两组值不是生产事实；调低会更早承担分片与折叠复杂度，调高会扩大热点尾延迟与页面负担。

评论量增长时按 thread 分片，确保 parent 与局部序在同一 owner；超大 thread 可按顶层子树切读索引，但写入仍验证根线程和 parent 版本。热评离线更新、在线拼入审核状态，不能让所有评论都 fan-out 到读者 inbox。

## 设计决定

本设计让 thread owner 原子验证 parent 并分配 `created_seq`，审核状态以单调版本 fence 编辑、缓存与回复。reaction 先条件设置唯一 membership，再写 outbox；分片计数幂等消费，定期与关系表抽样或全量纠偏。热评先过滤可见评论，再按计数、时间与质量派生。

超时重试使用 comment idempotency key 或 reaction desired state，重复 Like 返回既有 ACTIVE，不再次加计数。审核服务不可达时未知正文 fail-closed，已有墓碑绝不因缓存可用而展示。反选“reaction 只写分布式计数器”，在匿名、不可撤销且只需近似的浏览量中重新变优；对用户按钮和取消语义不成立。

也不选择把回复树拍平成全局时间流，因为会丢失 parent 关系；当产品明确只保留一层评论且不展示上下文时，平铺读取才更简单。

## 运行与演进

SLI 包括评论追加和反应切换延迟、membership/计数差异、计数 lag、热点 target 冲突、审核撤回后正文曝光、迟到版本拒绝、线程展开 `p99` 与孤儿回复数。过载时先延后通知、热评和精确公开数，再折叠深回复；用户关系写、审核和 parent 验证优先。

故障演练固定 C/v4 在 0ms 可见，10ms 删除为 v5，20ms 旧缓存任务写入，30ms 回复到达，40ms 热评任务读取旧计数。所有读区必须以 v5 隐藏正文和回复入口，旧填充拒绝；计数后台扣正不影响权限。恢复后重放删除和 outbox 幂等，演练核对用户自己的按钮与公开数允许短暂不同但最终收敛。

模型和排序升级影子比较热评差异，再灰度 ranking version；回滚不回退审核版本。跨地域以 thread 和 membership key 固定 owner，迁移用 epoch fence。删除覆盖正文、引用摘要和通知预览，保留最小审计墓碑；租户按热点 target 和通知量公平限额。

## 面试考察本质

给定“一个用户对一个目标的反应状态唯一，审核撤回立即优先于派生展示”这一不变量，因为分片计数和热评缓存不知道谁已取消、正文是否刚被封禁，候选人应推导出权威 membership/线程序与派生计数/rank 的分层，并在热点写争用、计数新鲜度、树读取成本和审核时效之间取舍。

优秀回答会让按钮读关系而非总数，写出 `Σ counter_i`，用双地域 Like 和 v4/v5 时间线解释对账与 fence，并指出线程序不能跨帖比较。常见误区是让 `+1/-1` 代表用户状态、把消息队列称为恰好一次，或删除 parent 后仍允许无条件回复。

二十分钟完成数据模型、唯一关系与线程不变量；四十分钟加入分片计数、分页和故障线；六十分钟再讨论审核、通知、跨地域与隐私删除。追问应始终区分用户事实、公开聚合和展示 rank。
