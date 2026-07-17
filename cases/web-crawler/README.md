# 网络爬虫

## 表面题目

设计网络爬虫，不只是“不断下载网页”。调用者提交种子 URL，系统发现链接、按规则调度请求、保存响应及解析结果，并能回答某 URL 的最近抓取版本。成功至少包含三层语义：没有把同一任务无限复制；没有以超出站点规则的速度访问外站；保存的内容能追溯到一次具体响应。题名会掩盖一个关键歧义：URL 是资源地址而非内容身份，同一 URL 会变化，多个 URL 也可能返回相同内容。

## 反问与边界

先问覆盖目标是全网发现、指定域归档，还是搜索索引更新；优化新页面发现率、重要页面新鲜度，还是历史快照完整性。需声明最大抓取速率、单 host 并发、robots 更新时限、重试上限、正文大小与 MIME 边界，以及 404、重定向和动态页面的处理。峰值不是 URL 总数，而是链接发现扇出与少数 host 偏斜。安全上必须阻断内网地址、凭据 URL 和 DNS rebinding；本题不承诺执行任意脚本，也不把对方返回 200 当作可公开使用内容的授权。

事实源分三类：站点当前 robots/响应是外部现实，frontier 是本系统的调度权威，内容仓保存不可变响应版本。派生的 URL 规范化表、host 队列和内容去重索引允许滞后，但必须携带策略、规范化器和抓取代际。需要业务校准抓取新鲜度目标与对外负载预算，不能凭经验虚构每秒页面数。

## 客观模型

最小命令是 `Discover(parent, rawURL)`、`Claim(host, now)`、`AuthorizeSend(task_lease, policy_generation)`、`Complete(task, response)` 和 `Fail(task, retryClass)`。任务键为 `(canonical_url, fetch_generation)`，状态经历 discovered、eligible、leased、completed 或 retryable。frontier 拥有任务状态；每个 host 的真实出口 send gate 拥有 `next_allowed_at`、当前 robots generation 与一次性短发送 token，只有它能让请求字节跨过网络边界；内容仓以响应版本写入。若平均页面发现 `f` 个新链接、去重通过率为 `u`、完成速率为 `μ`，队列变化为 `ΔQ/Δt = λ*f*u-μ`；大于零时再多 worker 也只会积压。

新鲜度可写成 `age = now-last_success_at`，调度分数综合重要度、age、失败退避和 host 可用时间。host H 的实际出口发送必须满足相邻请求间隔 `send(i+1)-send(i) ≥ politeness(H)`；预留时间或 worker 本地检查都不算发送。发送 token 绑定 task lease、robots generation、解析后的 DNS/IP、host 与截止时间，必须在 gate 跨越网络边界的同一临界区内一次性消费，过期或任一版本变化都不得发包。内容身份保存 `response_etag/last_modified/body_digest`，条件请求只能节省字节，304 才能证明该验证时刻未变化。日历 URL 产生无限日期参数是具体陷阱：每页 30 个新链接且 `u=1` 时，深度四已接近八十一万个任务。

## 必然约束

[DEDUCED:web-crawler-frontier-claim-must-be-atomic] 同一规范 URL 的到期任务必须由 frontier 原子声明，否则租约超时与重试会把重复抓取放大为外站负载。最小反例是两个 worker 同时读取“未抓取”，各自发送一次；即使完成时能去重内容，外站已经承受双倍请求。租约允许故障恢复，但 lease_id 与 generation 必须作为完成写入的 fencing token，旧 worker 迟到不能覆盖新结果。

[DEDUCED:web-crawler-policy-must-be-checked-at-send-time] robots 与主机礼貌间隔属于真实出口发送时约束，入队或 worker 发前检查都不能阻止策略变更、租约到期或进程暂停后的越界访问。反例是 worker A 校验 generation 7 并预留 host 时间后暂停，worker B 在新租约下合法发送，A 随后恢复；若 A 直接持 socket 发送，两次实际请求会挤在同一礼貌窗口。每个 host 的串行 send gate 必须在跨越网络边界的同一临界区校验一次性 token 截止时间、当前 task lease、robots generation、`next_allowed_at` 与重新解析的 DNS/IP，消费 token 并按实际发出时刻推进 host 时间；任一条件失效都不得发包。无法刷新策略或取得 gate 授权时暂停该 host，而不是把旧缓存或已预留时间当许可。

[DEDUCED:web-crawler-url-identity-cannot-replace-content-version] URL 身份既不能证明内容未变化，也不能识别不同 URL 的相同内容，抓取历史必须同时记录响应版本与内容摘要。规范化过强还会合并语义不同的查询参数；因此规范化规则版本化，URL 去重、响应版本和内容近重复检测是三个不同问题。

## 从简单方案演进

单进程 FIFO、内存 visited set 和串行下载是最简单正确基线，适合小站归档。若重启丢失 frontier，或 `queue growth > 0` 持续十五分钟，就把任务与抓取历史持久化。按 URL 全局 FIFO 会让大 host 占满头部；当单 host 占待处理任务超过百分之二十，或 host 等待 p95 超过目标两倍，演进为 host 分队列加全局公平调度。

再按 canonical URL 分片 frontier，并用有期限租约领取；真实网络出口仍按 host 归属到串行 send gate，worker 只提交候选请求，不能绕过 gate 自持 socket。它提高吞吐，却新增跨分片重复、租约抖动和热点域问题；租约到期只允许新 worker 重新申请一次性短 token，旧 token 即使持有者恢复也不能发送。当重复发送率超过业务外站预算或租约超时占完成量百分之一，应缩小任务批次、校准 token/lease 截止时间并调整 host gate 分片。内容索引用摘要做近重复合并能降低解析成本，但碰撞和模板页面误合并要求保留原响应。

刷新更快意味着更频繁地拉 robots、重访页面和写新版本，成本近似 `bytes/day = Σ(size_i / revisit_interval_i)`。当重要页面 age p95 超 SLO 且出口利用率低于七成，可缩短间隔；出口已饱和时只能降低低价值 URL 频率。未选“所有 worker 共享一个全局队列”，因为它无法表达 host 礼貌；若范围只有一个受控 host，它反而更简单。

## 设计决定

选择按 host 分队列、URL 分片持久 frontier、租约领取、真实出口串行 send gate 和不可变内容版本。发现路径先做安全解析与版本化规范化，再幂等 upsert。worker 持当前 task lease 向 host gate 申请绑定 lease、robots generation、DNS/IP、host 与短 deadline 的一次性 token；gate 在实际发送点重新解析并校验 DNS/IP，同时校验 token 未消费未过期、task lease 仍当前、robots generation 未变化且 `now ≥ next_allowed_at`。这些条件与“消费 token、让请求字节跨 socket、按实际发送时刻推进 `next_allowed_at`”处于同一 host 临界区；失效 token 不发，worker 也没有旁路出口。完成以 lease fencing 写抓取记录，迟到完成只作为旁路诊断。

超时按 DNS、连接、429/503、永久 4xx 分类退避；重试保持 task generation，不凭重试创建新逻辑任务。frontier 不可用时停止新发送而不从本地缓存猜测权限。内容仓短暂不可用时可以延长租约，但超过上限就释放重试，接受可能重复网络请求并计量。布隆过滤器只作负向加速，不作永久 visited 权威，因为误判会漏抓。

## 运行与演进

核心 SLI 是重要 URL 新鲜度、有效新内容/请求、重复发送率、robots 违反数、实际 host 间隔最小值、send gate 等待、过期/旧 lease token 的出口拒绝数、frontier age、429 比例与每 GiB 新内容的出口成本。过载先暂停低优先级重访，再降低新链接深度，最后停止新 host；绝不绕过 gate 或放宽礼貌与安全边界。

演练时间线：10:00 worker A 取得 task lease generation 7 和一次性 token T7，T7 截止 10:00:05，但 A 在进入真实出口前暂停；10:00:05 lease 与 T7 到期，worker B 取得 generation 8；10:00:10 B 的 T8 在 send gate 同时通过当前 task lease、robots generation、DNS/IP 与 `next_allowed_at` 校验，被一次性消费并实际发出，gate 由 10:00:10 推进下一允许时间；10:00:11 worker A 恢复，T7 因过期且绑定旧 lease 被 gate 在 socket 前拒绝，不能挤在 B 后发送。另演练 robots generation 在授权后、实际出口前变化，旧 token 同样失效。灰度新规范化器时双算 canonical key，监测合并/拆分差异；回滚保留旧键映射。待压测切换指标至少包括 frontier 写入 p99 超预算且分片 CPU 连续七成，以及同 host 429 率超过基线三倍。待业务校准的是可接受 age、token/lease 截止时间与重复请求预算。

## 面试考察本质

本题本质是：在“不得重复淹没外站且实际出口必须遵守当前发送策略”的不变量下，因为外部内容、robots、DNS 和暂停 worker 是否仍会发包都无法即时获知，候选人必须推导 frontier 租约、一次性短 token、host 串行 send gate、公平调度和版本化内容之间的取舍，再按新鲜度价值、gate 协调成本与对外负载风险分配抓取预算。它不是“队列加 worker”题，完成写 fencing 也不能替代真实出口 fencing。

优秀回答会先区分 URL、任务和内容版本，给出双 worker 最小反例，并说明派生索引 lag 与刷新成本。常见误区是布隆过滤器直接决定永不抓取、仅入队时看 robots、用内容去重掩盖网络重复。追问可进入 DNS rebinding、canonical 冲突、租约 fencing、增量重访和站点删除。20 分钟讲模型与 host 调度，40 分钟补故障和演进，60 分钟再讨论近重复、跨地域出口与证据边界。
