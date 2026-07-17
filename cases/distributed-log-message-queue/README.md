# 分布式日志与消息队列

## 表面题目

设计分布式日志与消息队列，表面流程是生产者追加消息、代理复制保存、消费者按主题读取并确认。真正的状态变化是某条记录进入某个分区的已提交日志前缀，并获得该分区内唯一偏移；成功必须区分生产确认、记录可恢复、消费者已读和外部业务副作用完成。题名也掩盖了“工作队列”与“可重放日志”的差异：前者常强调一个组内分摊任务，后者允许多个消费组按各自进度反复读取。本文把持久分区日志作为事实源，把消费组偏移仅当恢复游标，不承诺跨分区总序或外部处理恰好一次。

## 反问与边界

先问生产成功要覆盖进程、机器、可用区还是区域故障，ACK 前需要多少持久副本；再问同一业务键是否要求有序、能否重分区、保留多久，以及消费组落后到保留边界时是阻塞生产、扩容、丢弃还是从快照重建。吞吐要按峰值字节率、单分区上限、消息大小分布和热点键表达，不能只报主题平均每秒条数。还需明确消费者是拉取还是推送、重试是否允许重复、毒消息如何隔离、租户配额及敏感载荷的加密和删除边界。

目标 SLO 至少分为 append 延迟、已确认记录恢复率、最老消费 lag 年龄和重放吞吐。排序域明确为单个 partition；两个 partition 的偏移不可比较。重放边界是仍在 retention 内的已提交日志与该组最后提交 offset。背压可以出现在 producer 配额、broker append 队列、复制 lag、consumer credit 和下游 sink。非目标是用队列提交自动证明数据库、邮件或支付只执行一次，也不为所有键建立高吞吐全局顺序。

## 客观模型

最小接口为 `Append(topic,key,event_id,payload,producer_epoch,sequence)`、`Fetch(group,partition,offset,limit)`、`CommitOffset(group,generation,partition,next_offset)`。核心状态是 `partition -> {leader_epoch,next_offset,replicated_log,high_watermark}` 与 `group -> {generation,assignment,committed_offsets}`。分区复制组拥有日志和高水位；group coordinator 拥有 assignment 与提交权世代；消费者本地处理状态和外部数据库不属于它们。一个 generation 中，同一 group 的同一 partition 只有一个有效提交 owner。

不变量是：已确认记录在声明故障范围内可恢复；同一分区已提交前缀的 offset 唯一递增；旧 leader epoch 和旧 group generation 不能继续提交；保留删除只依赖策略与安全水位，不能把一个组的 ACK 当成所有读者删除许可。入口字节率 `B`、复制因子 `R`、保留时长 `T`、压缩和索引放大 `A` 时，持久容量近似 `V=B×R×T×A`。生产率 `λ` 大于消费率 `μ` 时，lag 在 `Δt` 内增长 `max(0,(λ-μ)×Δt)`；条数 lag 还必须配合最老消息年龄，因为大小和处理成本可能高度倾斜。

热点 key 必须落在一个顺序 owner 上，因此单 key 吞吐受单 partition 的复制路径约束。增加 partition 提升不同 key 的并行度，却会改变重键后的顺序域和迁移语义。丢失边界在 ACK 早于足够副本持久化、应用写入本地前崩溃或保留期先于消费者追上；重复边界在 producer ACK 丢失后的重试，以及 consumer 完成外部副作用但尚未提交 offset 的崩溃窗口。

## 必然约束

[DEDUCED:distributed-log-message-queue-order-is-only-per-partition] 分区 leader 能为其接受的记录分配单调偏移，所以 offset 证明单分区追加顺序；两个 leader 没有共同线性化点。最小反例是 P0 的 offset 900 与 P1 的 offset 20：数字大小既不代表墙钟先后，也不代表业务因果，网络延迟甚至可让后发生的记录先被消费。若要求订单内有序，应以订单键稳定路由到同一 partition；只有低吞吐、所有事件确需统一审计序列时，单全局日志才适用。

[DEDUCED:distributed-log-message-queue-durable-offset-is-consumer-progress-not-message-acknowledgement] group 在处理 offset 900 后提交 901，只表示该组愿意从 901 恢复。反例是处理器先写数据库，提交偏移前崩溃，接管者从 900 重放并再次写库；反过来若先提交再写库，崩溃会永久漏写。其他 group 仍可能停在 100，保留策略也独立。因此必须以业务 `event_id` 在 sink 原子去重，或诚实接受至少一次，不能用一个 offset 同时证明消息删除和外部副作用完成。

[DEDUCED:distributed-log-message-queue-replication-ack-bounds-log-durability] 若 leader 在记录只存在本机时 ACK，故障后新 leader 无法从任何信息重建该字节。最小反例是 epoch 7 的 leader 接收 R、返回成功、随后磁盘损坏，异步 follower 尚未复制；客户端已有成功事实而集群没有记录。确认必须等待声明故障域内足够副本持久化并推进高水位。若业务接受机器故障时少量已确认消息丢失，可以降低 ACK 等级换延迟，但契约必须明确。

## 从简单方案演进

最简单正确基线是单节点追加文件加单消费者游标，清楚但无故障容忍和横向并行。当磁盘恢复时间超出可用性目标，先引入分区内复制、leader epoch 与 quorum ACK；它解决单机丢失，却增加副本网络、选主和未提交尾部截断。当热点 partition 持续入口超过安全复制吞吐的 70%，或 append `p99` 超过生产 SLO 的 40%，按业务键增加 partition 或重键，代价是不能保持跨新旧分区总序。

消费者增加后，以 group generation 分配 partition 并提交独立 offset。它解决并行处理，却引入 rebalance 暂停、旧 owner 提交和重复处理。若最老 lag 超过保留窗口的 50%，或磁盘使用超过保留预算的 75%，先限制重放流量、扩消费者或延长保留；仍追不上时从版本化快照和新 offset 显式重建，不静默跳过。若 producer 重试重复率触及业务容忍上限，则加入稳定 producer identity、epoch 与 sequence；这只去除追加重试重复，不能覆盖 consumer 的外部 sink。

上述 70%、40%、50% 和 75% 均是待压测与演练校准的初始策略参数：较低阈值预留故障转移余量但提前增加分区和成本，较高阈值提高利用率却缩短追赶与迁移窗口。反选“所有事件共用单全局日志”在跨所有键确需唯一审计顺序且吞吐较低时重新变优；否则它把无关生产者锁在一个协调点。

## 设计决定

本设计按稳定业务键选择 partition，leader 只有在 quorum 持久化后才确认，读取只暴露高水位内前缀。producer 使用带 epoch 的递增 sequence 消除同一会话重试追加；跨 epoch 仍以业务事件 ID 判定。consumer 通过拉取信用形成背压，group coordinator 用 generation fencing 旧 owner，offset 提交仅在处理完成后推进。毒消息经过有次数上限的重试后进入带原始 offset 的隔离流，不能让一个 partition 永久停摆。

正常路径保证分区内日志耐久和可重放，不保证外部副作用 exactly-once。支持条件写的 sink 用 `(consumer, event_id)` 原子写结果与去重标记；不支持的 HTTP 或邮件 sink 采用至少一次并暴露重复风险。超时不等于失败：producer 以相同 sequence 查询或重试，consumer 不在未知时推进 offset。过载先限制低优先租户与大规模历史重放，再降低生产信用，不能删除已确认前缀来伪装恢复。

未选择“每条消息消费后立即物理删除”，因为它破坏多组独立重放和审计；只有单消费者、不可重放的瞬时任务队列才更简单。也未把 broker 与所有业务数据库做分布式事务，因为参与者和延迟不可控；资金类少数路径可用本地 inbox/outbox 将原子边界落到单一状态 owner。

## 运行与演进

关键 SLI 是各 partition append `p99`、副本高水位 lag、未提交尾部字节、每组最老 lag 年龄、rebalance 时长、producer sequence 重复率和保留余量。看板必须按 partition 和租户拆分，主题平均值会掩盖热点。过载顺序是限制历史重放、压缩大批量 producer、降低拉取信用、最后拒绝新增写；已确认记录不能因降级丢弃。跨租户要限制分区数、字节率和磁盘保留，载荷加密密钥与删除策略按租户隔离。

故障演练时间线：0 ms，epoch 7 leader 将 R 复制到 quorum 并推进高水位 900；5 ms，producer ACK 丢失；20 ms，相同 identity/sequence 重试，leader 返回既有 offset，若未启用 sequence 则 R' 会成为合法业务重复；100 ms，consumer 已写外部库但尚未提交 901；120 ms 崩溃，generation 9 接管者从 900 重放，sink 以 event ID 去重。演练分别验证记录未丢、旧 owner 被 fence、外部结果不重复或被明确标记。

扩分区先发布路由版本，再让 producer 双读路由但单写新 owner；旧记录仍按旧 partition 重放，不能靠比较不同分区 offset 合并。回滚停止新路由，不重写既有偏移。区域灾备必须说明复制 ACK 是否覆盖远端；若异步远端，区域丢失窗口应作为可观察数据风险而非“高可用”口号。

## 面试考察本质

给定“已确认记录在单分区内可恢复且拥有唯一偏移”的不变量，因为 broker 无法知道 consumer 崩溃前外部副作用是否完成，且不同 partition 没有共同排序点，候选人应推导出复制确认、局部顺序、消费恢复与业务去重四个独立边界。最终取舍依据单键顺序、吞吐、保留成本和重复/丢失业务风险，而不是一句使用某个队列产品。

优秀信号包括写出 `B×R×T×A`、同时观察 lag 条数与年龄、区分 high watermark 和 group offset、指出 ACK 丢失会产生追加或处理重复，并定位背压从 producer 到 sink 的每一段。常见误区是认为 partition 越多单 key 越快、把 offset commit 当消息已被所有人消费、或无条件宣称 exactly-once。

二十分钟回答应完成接口、分区日志、复制 ACK 和 group offset；四十分钟加入 producer sequence、rebalance fencing、保留与重放；六十分钟再讨论热点重键、跨区域耐久、租户公平和外部 sink 去重。追问可沿 offset 900 的 ACK 丢失、数据库写后崩溃和慢组逼近 retention 展开，始终要求说明排序域、重放边界、状态 owner、背压位置及丢失或重复的准确窗口。
