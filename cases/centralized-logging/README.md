# 集中式日志

## 表面题目

设计集中式日志系统，表面流程是各节点输出记录、agent 上传、中央存储建立索引并支持搜索。真正的状态变化是带稳定来源序号的原始记录从本地未确认 spool 进入中央已持久 segment，随后由派生索引变得可检索，并最终按保留或删除策略退出。成功必须区分“应用尝试写日志”“agent 已缓冲”“中央已确认”和“索引已可见”；空查询不能自动证明事件没有发生。审计日志、调试日志和高吞吐访问日志对丢失、阻塞、保留与授权的风险不同，不能共享一个静默丢弃策略。

## 反问与边界

先问日志等级和用途：安全审计是否必须保留，调试日志能否采样，应用在 agent 不可用时应阻塞还是继续；再问可搜索新鲜度、retention、删除证明、冷热层查询延迟和字段授权。容量按原始字节率、压缩、复制、索引字段与 token 放大、租户倾斜和离线时长估算，不能只按每秒行数。还要明确结构化 schema、超长行、二进制载荷、敏感字段脱敏、租户隔离和查询导出审计。

排序域是单 source 的 `ingest_seq` 或单 segment 追加顺序，event time 可因缓冲和时钟漂移乱序；跨节点日志没有自然总序。重放边界是仍在 agent spool 的未确认记录和 central retention 内的 segment。背压链是应用输出、agent buffer、网络、ingest quota、segment writer、index queue 与查询扫描。owner 分层：agent 拥有未 ACK spool，central segment store 拥有已确认原始记录，retention controller 拥有删除世代，索引器只拥有派生可搜索视图。

## 客观模型

最小记录为 `(tenant,source,ingest_seq,event_time,severity,schema_version,body,retention_class)`；接口包括 `AppendLocal(record)`、`Ingest(batch,first_seq,last_seq)`、`Search(query,time_range,index_watermark)` 和 `Delete(policy_generation)`. agent 先持久化 spool，中央将 batch 写入不可变 segment 与 manifest 后确认；索引异步消费 segment，查询返回覆盖的 segment 范围与 index watermark。稳定 `(source,ingest_seq)` 让断线重传可去重。

不变量是：中央已确认记录在 retention 内可按稳定 ID 找回，或能证明它由哪一策略世代删除；搜索结果不得越过租户和字段授权；倒排索引损坏不能修改原始证据；未确认记录的 drop 必须计量并暴露。原始速率 `B`、保留 `T`、复制 `R`、压缩系数 `c`、索引放大 `I` 时，容量近似 `V=B×T×R×(c+I)`。agent 离线 `d` 秒、可保留速率 `b` 时，spool 至少需要 `b×d` 字节。

全文 token、高基数字段和长 retention 共同主导成本；能收下日志不代表能低延迟搜索。丢失边界在应用于 stdout 前崩溃、agent spool 溢出和中央 ACK 前错误清理；重复边界在 ACK 丢失后的 batch 重传。索引暂时缺失会造成查询假阴性，查询必须显示索引覆盖，而不是把未索引段解释为不存在。

## 必然约束

[DEDUCED:centralized-logging-searchability-is-a-retention-contract] “可搜索”要求原始 segment 仍存活、对应索引可用或可重建、授权允许且查询知道覆盖水位。最小反例是原始数据保留三十天而热索引仅七天：第八天关键词查询为空，并不表示记录不存在。结论是查询契约必须携带冷热层、索引版本和 retention 范围；永久审计与交互搜索是两种成本。若用户只按稳定 ID 取原文，可不维持全文索引，但不能宣称任意搜索。

[DEDUCED:centralized-logging-ingest-ack-bounds-loss-semantics] agent 在中央持久 ACK 前清理本地 spool，网络分区就会制造无法区分“未发生”和“已丢失”的空洞。最小反例是安全日志以 5 MB/s 产生、spool 100 MB，链路中断 30 秒后必然超出容量；静默淘汰最旧十秒会让调查者误判。必须为各等级选择阻塞、采样、保留或明确 drop，并让中央看到缺口。TCP 写成功也不等于 segment manifest 已提交。

[DEDUCED:centralized-logging-index-is-derived-from-immutable-records] 索引只保存从 token 或字段到记录位置的映射，可延迟、损坏或因 analyzer 升级改变；它不包含完整证据和删除原因。若把索引作为唯一事实，重建差异无法分辨是记录被篡改还是分词变化。原始不可变 segment 与内容摘要必须是 owner，索引用版本和 watermark 派生。只有无审计需求、原始内容可丢且索引本身就是产品数据时，才可省略原始层。

## 从简单方案演进

基线是每台机器本地滚动文件，故障域清楚但跨节点调查困难且实例销毁会丢失。当本地保留短于事故发现时间，先加入持久 spool agent 与中央不可变 segment；它解决汇聚，却新增网络分区、重复上传和租户配额。当 agent spool 使用超过 70%，或 central rejection 超过 1%，先采样 debug、聚合重复行、限制非关键字段；审计日志无法保留时必须显式阻塞或触发严重降级，不能静默丢弃。

查询扫描变慢后，为稳定字段和 token 建立分段索引，并将旧 segment 放入冷层。若索引字节与压缩原始字节比超过 3:1，或典型查询扫描 `p95` 超过交互 SLO 的 60%，把低价值字段转为按需解析，保留字段白名单与索引水位。索引器落后时先保护 ingest 证据，再降级搜索新鲜度；不能为了面板“绿色”丢原始记录。

70%、1%、3:1 和 60% 是待容量测试、断网演练与调查 SLO 校准的初始参数。较低阈值增加冷存与采样成本，较高阈值缩短审计缓冲和查询余量。反选“只存结构化指标”在稳定低维、无需调查单请求的服务中成本更低；未知字段、追责与全文调查需要原始日志。

## 设计决定

本设计在 source agent 先按 severity 与 retention class 写持久 spool，再批量上传；central 仅在 segment 数据、校验和与 manifest 一致提交后 ACK sequence 范围。ACK 丢失时 agent 重传，中央以 `(source,ingest_seq)` 去重。索引异步读取不可变 segment，查询同时返回结果、原始覆盖和 index watermark；必要时允许扫描未索引热段或异步取冷段。

背压按等级执行：先抛弃可重建 debug 派生字段和重复行，再采样低优先 access log，保留安全审计；若审计也无法写入，应用依据契约阻塞敏感操作或进入明确 fail-closed，而非伪造完整。segment 内只声明 ingest sequence，event-time 查询允许乱序。删除由版本化 retention policy 产生 tombstone 与审计记录，索引和缓存随后收敛。

未选择应用直接同步写中央，因为网络故障会把日志可用性放入每个请求关键路径；只有必须 fail-closed 的少数审计操作才同步确认。也未让索引写成功作为 ingest ACK，因为索引吞吐和重建应独立于证据耐久。

## 运行与演进

SLI 包括 agent spool 使用率、sequence gap、ingest 接受/拒绝字节、segment commit 延迟、重复上传率、index lag、搜索覆盖率、冷热查询 `p95`、drop 按等级计数和删除传播延迟。空结果页显示数据 retention、索引水位与 source 缺口。租户按 ingest、索引字段、查询扫描和保留配额；字段级授权、脱敏、加密密钥与导出审计不得只依赖前端。

故障时间线：0 s，agent 将 record 701 落入 spool；20 ms，central 持久 segment S 和 manifest 后 ACK 701；30 ms，索引只覆盖到 650，搜索标注落后；2 min，网络断开，debug 按策略采样而审计占用保留 spool；恢复后以稳定 sequence 重传去重。若应用在写 stdout 前崩溃，该记录不在任何重放边界，系统只能通过其他信号发现，不能补造。

schema 和 analyzer 升级先用新索引版本影子构建，对同一 segment 比较覆盖，再切查询路由；回滚继续读旧索引，原始段无需改写。retention 缩短先输出影响字节、租户和查询范围，灰度删除并核对 tombstone。区域灾备分别验证未 ACK spool、已确认 segment 和索引重建的 RPO/RTO。

## 面试考察本质

给定“已确认原始日志在保留期内可按授权检索，或能解释其删除”的不变量，因为采集端不知道网络分区持续多久、索引端也不知道未来哪些字段会被调查，候选人应推导出本地 spool、不可变原始段、派生索引、分级背压与 retention 契约。主导取舍是证据完整性、搜索新鲜度、索引放大和应用阻塞风险。

优秀信号包括写出 `B×T×R×(c+I)`、计算离线 spool、区分 ingest ACK 与 index visibility、显示 sequence gap 和查询 watermark，并让安全日志与 debug 使用不同丢失策略。常见误区是应用 fire-and-forget 后声称无丢失、把 TCP 成功当持久化、空查询等同未发生，或索引过载时先删除原始段。

二十分钟回答应完成 agent、segment、索引和查询；四十分钟加入 spool、重传去重、冷热 retention 与授权；六十分钟再讨论 schema 演进、删除证明、区域灾备和成本隔离。追问可用 5 MB/s、100 MB spool、断网 30 秒的反例，要求说明排序域、重放边界、背压、state owner 与丢失/重复位置。
