# 集中式日志

## 表面题目

设计集中式日志系统，表面流程是各节点输出记录、agent 上传、中央存储建立索引并支持搜索。真正的状态变化是带稳定来源序号的原始记录从本地未确认 spool 进入中央已持久 segment，随后由派生索引变得可检索，并最终按保留或删除策略退出。成功必须区分“应用尝试写日志”“agent 已缓冲”“中央已确认”和“索引已可见”；空查询不能自动证明事件没有发生。审计日志、调试日志和高吞吐访问日志对丢失、阻塞、保留与授权的风险不同，不能共享一个静默丢弃策略。

## 反问与边界

先问日志等级和用途：安全审计是否必须保留，调试日志能否采样，应用在 agent 不可用时应阻塞还是继续；再问可搜索新鲜度、retention、删除证明、冷热层查询延迟和字段授权。容量按原始字节率、压缩、复制、索引字段与 token 放大、租户倾斜和离线时长估算，不能只按每秒行数。还要明确结构化 schema、超长行、二进制载荷、敏感字段脱敏、租户隔离和查询导出审计。

排序域是单 `(tenant,source,source_epoch)` 的 `ingest_seq` 或单 segment 追加顺序，event time 可因缓冲和时钟漂移乱序；跨节点日志没有自然总序。重放边界是仍在 agent spool 的未确认记录和 central retention 内的 segment。背压链是应用输出、agent buffer、网络、ingest quota、segment writer、index queue 与查询扫描。owner 分层：agent 拥有未 ACK spool，central source registry 签发并 fencing source epoch，segment store 拥有已确认原始记录，retention controller 拥有删除世代，索引器只拥有派生可搜索视图。

## 客观模型

最小记录为 `(tenant,source,source_epoch,ingest_seq,event_time,severity,schema_version,body,retention_class)`；接口包括 `RegisterSource(tenant,source,durable_installation_id)`、`AppendLocal(record)`、`Ingest(batch,source_epoch,first_seq,last_seq)`、`Search(query,time_range,index_watermark)` 和 `Delete(policy_generation)`. agent 首次安装时由中央 source registry 通过 CAS 签发单调 `source_epoch`，把它与下一序号持久化到本地 spool 元数据；普通重启必须恢复该 epoch，不能从 0 自行猜测。重装或并发实例只能注册新 epoch；新 epoch 激活时旧 epoch 被 fencing，旧实例不能继续生成记录，只能在已声明的 `sealed_last_seq` 内排空旧 spool，否则中央拒绝并记录显式 gap。

agent 先持久化 spool，中央将 batch 写入不可变 segment 与 manifest 后确认；索引异步消费 segment，查询返回覆盖的 segment 范围与 index watermark。稳定 `(tenant,source,source_epoch,ingest_seq)` 让断线重传可去重，同时避免 agent 重装、磁盘恢复或并发实例把序号重置后的新日志误判为旧重复。若本地 epoch 元数据不可恢复，agent 必须重新注册而不是复用旧 epoch。

不变量是：同一租户、来源、epoch 和序号只对应一条内容摘要一致的记录；已被新 epoch fencing 的旧实例不能铸造新序号。中央已确认记录在 retention 内可按稳定 ID 找回，或能证明它由哪一策略世代逻辑隐藏、何时达到物理或密码学不可恢复；搜索结果不得越过租户和字段授权；倒排索引损坏不能修改原始证据；未确认记录的 drop 必须计量并暴露。原始速率 `B`、保留 `T`、复制 `R`、压缩系数 `c`、索引放大 `I` 时，容量近似 `V=B×T×R×(c+I)`。agent 离线 `d` 秒、可保留速率 `b` 时，spool 至少需要 `b×d` 字节。

全文 token、高基数字段和长 retention 共同主导成本；能收下日志不代表能低延迟搜索。丢失边界在应用于 stdout 前崩溃、agent spool 溢出和中央 ACK 前错误清理；重复边界在 ACK 丢失后的 batch 重传。索引暂时缺失会造成查询假阴性，查询必须显示索引覆盖，而不是把未索引段解释为不存在。

## 必然约束

[DEDUCED:centralized-logging-searchability-is-a-retention-contract] “可搜索”要求原始 segment 仍存活、对应索引可用或可重建、授权允许且查询知道覆盖水位。最小反例是原始数据保留三十天而热索引仅七天：第八天关键词查询为空，并不表示记录不存在。结论是查询契约必须携带冷热层、索引版本和 retention 范围；永久审计与交互搜索是两种成本。若用户只按稳定 ID 取原文，可不维持全文索引，但不能宣称任意搜索。

[DEDUCED:centralized-logging-ingest-ack-bounds-loss-semantics] agent 在中央持久 ACK 前清理本地 spool，网络分区就会制造无法区分“未发生”和“已丢失”的空洞；若去重只用会在重装时归零的 `(source,ingest_seq)`，新日志还会被静默当成旧重复。最小反例是安全日志以 5 MB/s 产生、spool 100 MB，链路中断 30 秒后必然超出容量；静默淘汰最旧十秒会让调查者误判。必须为各等级选择阻塞、采样、保留或明确 drop，并让中央看到缺口；去重键必须包含租户和由 registry 持久签发、可 fencing 的 source epoch。TCP 写成功也不等于 segment manifest 已提交。

[DEDUCED:centralized-logging-index-is-derived-from-immutable-records] 索引只保存从 token 或字段到记录位置的映射，可延迟、损坏或因 analyzer 升级改变；它不包含完整证据和删除原因。若把索引作为唯一事实，重建差异无法分辨是记录被篡改还是分词变化。原始不可变 segment 与内容摘要必须是 owner，索引用版本和 watermark 派生。只有无审计需求、原始内容可丢且索引本身就是产品数据时，才可省略原始层。

## 从简单方案演进

基线是每台机器本地滚动文件，故障域清楚但跨节点调查困难且实例销毁会丢失。当本地保留短于事故发现时间，先加入持久 spool agent 与中央不可变 segment；它解决汇聚，却新增网络分区、重复上传和租户配额。当 agent spool 使用超过 70%，或 central rejection 超过 1%，先采样 debug、聚合重复行、限制非关键字段；审计日志无法保留时必须显式阻塞或触发严重降级，不能静默丢弃。

查询扫描变慢后，为稳定字段和 token 建立分段索引，并将旧 segment 放入冷层。若索引字节与压缩原始字节比超过 3:1，或典型查询扫描 `p95` 超过交互 SLO 的 60%，把低价值字段转为按需解析，保留字段白名单与索引水位。索引器落后时先保护 ingest 证据，再降级搜索新鲜度；不能为了面板“绿色”丢原始记录。

70%、1%、3:1 和 60% 是待容量测试、断网演练与调查 SLO 校准的初始参数。较低阈值增加冷存与采样成本，较高阈值缩短审计缓冲和查询余量。反选“只存结构化指标”在稳定低维、无需调查单请求的服务中成本更低；未知字段、追责与全文调查需要原始日志。

## 设计决定

本设计在 source agent 先按 severity 与 retention class 写持久 spool，再批量上传；central 仅在 segment 数据、校验和与 manifest 一致提交后 ACK sequence 范围。ACK 丢失时 agent 重传，中央以 `(tenant,source,source_epoch,ingest_seq)` 去重并校验内容摘要。epoch 由中央 registry 签发且随 spool 元数据持久恢复；新 epoch 通过 CAS 激活并 fencing 旧实例，旧 epoch 只可重放注册时封存的序号上界。索引异步读取不可变 segment，查询同时返回结果、原始覆盖和 index watermark；必要时允许扫描未索引热段或异步取冷段。

背压按等级执行：先抛弃可重建 debug 派生字段和重复行，再采样低优先 access log，保留安全审计；若审计也无法写入，应用依据契约阻塞敏感操作或进入明确 fail-closed，而非伪造完整。segment 内只声明单 source epoch 的 ingest sequence，event-time 查询允许乱序。

删除由版本化 retention policy 产生 tombstone 与审计记录。tombstone 生效并被查询入口、索引和缓存观察后，只能声明“逻辑不可见”：正常授权查询和导出不再返回该记录，这不等于底层字节已经消失。后台 compactor 按 tenant 与 retention class 重写混合 segment 或回收整段，并等待原始段、索引、查询缓存、冷层及其副本分别确认目标 generation；只有这些在线副本都完成回收，才达到“在线物理删除”。备份和离线副本遵守公开的到期边界，在恢复时先重放 tombstone；若法规要求缩短可恢复窗口，则以每租户、每版本数据密钥加密，并在确认无其他引用后销毁密钥，才可声明“物理或密码学不可恢复”。系统不承诺 tombstone 提交即瞬时全域擦除。

未选择应用直接同步写中央，因为网络故障会把日志可用性放入每个请求关键路径；只有必须 fail-closed 的少数审计操作才同步确认。也未让索引写成功作为 ingest ACK，因为索引吞吐和重建应独立于证据耐久。

## 运行与演进

SLI 包括 agent spool 使用率、epoch fencing 拒绝数、sequence gap、ingest 接受/拒绝字节、segment commit 延迟、重复上传率、index lag、搜索覆盖率、冷热查询 `p95`、drop 按等级计数、tombstone 查询传播延迟和各存储层物理回收积压。空结果页显示数据 retention、索引水位与 source 缺口。删除审计分别记录“逻辑不可见”的观察 generation，以及原始段、索引、缓存、冷层、副本、备份到期或密钥销毁都满足后的“物理或密码学不可恢复”时间。租户按 ingest、索引字段、查询扫描和保留配额；字段级授权、脱敏、加密密钥与导出审计不得只依赖前端。

故障时间线：0 s，租户 T 的 agent A 在 epoch 7 将 record 701 落入 spool；20 ms，central 持久 segment S 和 manifest 后 ACK `(T,A,7,701)`；30 ms，索引只覆盖到 650，搜索标注落后；2 min，网络断开，debug 按策略采样而审计占用保留 spool；恢复后以完整稳定 ID 重传去重。若 A 被重装，registry 签发 epoch 8 并 fencing epoch 7，新的 sequence 1 不会与 `(T,A,7,1)` 冲突；epoch 7 仅能排空预先封存的尾部。若应用在写 stdout 前崩溃，该记录不在任何重放边界，系统只能通过其他信号发现，不能补造。

schema 和 analyzer 升级先用新索引版本影子构建，对同一 segment 比较覆盖，再切查询路由；回滚继续读旧索引，原始段无需改写。retention 缩短先输出影响字节、租户和查询范围，灰度发布 tombstone 并核对逻辑不可见，再跟踪按 tenant/retention class 的 segment 重写、各层回收 ACK、备份到期或密钥销毁，不能把第一阶段报告成全域擦除。区域灾备分别验证未 ACK spool、已确认 segment、删除 generation 和索引重建的 RPO/RTO。

## 面试考察本质

给定“已确认原始日志在保留期内可按授权检索，或能解释其逻辑不可见与最终不可恢复阶段”的不变量，因为采集端不知道网络分区持续多久、索引端也不知道未来哪些字段会被调查，候选人应推导出持久 source epoch、本地 spool、不可变原始段、派生索引、分级背压与 retention 契约。主导取舍是证据完整性、搜索新鲜度、索引放大、删除传播窗口和应用阻塞风险。

优秀信号包括写出 `B×T×R×(c+I)`、计算离线 spool、用 `(tenant,source,source_epoch,ingest_seq)` 处理重装与并发实例、区分 ingest ACK 与 index visibility、区分逻辑不可见与物理/密码学不可恢复，并让安全日志与 debug 使用不同丢失策略。常见误区是应用 fire-and-forget 后声称无丢失、用可重置序号去重、把 TCP 成功当持久化、空查询等同未发生，或把 tombstone 当成瞬时全域擦除。

二十分钟回答应完成 agent、segment、索引和查询；四十分钟加入持久 epoch、spool、重传去重、冷热 retention 与授权；六十分钟再讨论 schema 演进、分阶段删除证明、备份/密钥边界、区域灾备和成本隔离。追问可用 5 MB/s、100 MB spool、断网 30 秒以及 agent 重装序号归零的反例，要求说明排序域、重放边界、背压、state owner 与丢失/重复位置。
