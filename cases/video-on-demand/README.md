# 视频点播

## 表面题目

设计视频点播不是“把大文件放到缓存里”。用户选择标题后，系统要在启动时延内返回可播放清单，播放器根据设备与网络选择码率，随后连续取得音视频分片；上传者则要把源文件转成多种 rendition，经审核后一次性发布。成功至少分三层：源文件已耐久、某个版本已完成处理、至少一个与客户端兼容且完整的 release 已注册并能读到全部必需分片。一次播放应固定在一个不可变时间轴上，不能因为后台重转码而在中途混入另一版。

题名容易掩盖两个问题：首帧等待与播放中卡顿受不同链路支配；“更新视频”究竟覆盖旧对象，还是生成新版本并原子发布。本文选择不可变 source、segment 与 release manifest generation；标题目录可同时保留面向不同协议、编解码器与 DRM 能力的多个 eligible release，而不是永远只有一个全局 current。删除、地域版权和成人内容策略是独立访问边界，不能靠对象 URL 难猜来代替。

## 反问与边界

先问清片长、源文件大小、编码梯度、热门度偏斜、峰值并发、地域和设备分布。体验目标应分别给出点击到首帧 `p95`、播放卡顿率、清晰度切换频率、拖动命中延迟和可播放发布时限；“99.9% 可用”不足以描述看了十分钟却卡三次。还要确认字幕、音轨、广告标记是否与视频同版，允许播放器落后最新发布版本多久，内容下架是立即拒绝新会话还是还要终止已有会话。

容量不能只用标题数估算。设日新增源时长为 `H` 秒，平均源码率 `r0`，输出梯度码率为 `r1..rk`，保留 `d` 份，则日新增字节约为 `H×(r0+Σri)×d/8`，另加封装与索引。峰值出口约为 `Σ concurrent_i×selected_bitrate_i`，热门度长尾决定缓存命中。示例：十万并发各看 4 Mb/s 就是约 400 Gb/s，平均日流量无法解释晚间峰值。数字是容量演算示例，真实阈值须由业务压测校准。

## 客观模型

核心实体是 `SourceGeneration`、`Rendition`、`Segment`、`ReleaseManifest`、`TitleReleaseSet`、`PlaybackSession` 和 `PlaybackLicense`。源、分片和 manifest 都不可变并以摘要校验；manifest 除统一时间轴、分片序号、URI、长度与 digest 外，还冻结协议与 schema version、codec/profile 集合、DRM scheme 和 key generation。发布事务先验证必需音轨、字幕、密钥引用与每个分片可读，再把该 release CAS 加入标题的 eligible 集合；旧 release 在仍有受支持客户端或未到迁移截止期时继续可选。

播放网关接收客户端声明并服务端校验的 protocol、codec、DRM/CDM 与安全级别能力，从全部完整、未撤销 release 中选择“最新兼容”generation；它不能先选一个全局 current 再祈祷旧设备能播。会话令牌绑定 title、manifest generation、策略 generation、设备和过期时间。DRM license 进一步绑定 manifest generation、key generation、device id、权限范围与 expiry；首次签发和每次续期都读取当前 entitlement/deny generation，旧权利缓存不能自行续命。

若分片时长为 `s`、往返为 `L`、并行预取 `p`、所选码率 `r`、可用吞吐 `b(t)`，粗略首帧下界包含 `manifest RTT + ceil(initial_buffer/s)/p` 批分片往返与首片下载；稳态只有在窗口内 `∫b(t)dt ≥ r×window` 时缓冲才不持续下降。短分片降低 seek 粒度和切换延迟，却把请求数放大到每观看小时约 `3600/s`，清单也随之增大。热门标题和首分片形成热点，冷门长尾则主要消耗源站与目录元数据。

版本不变量是：一个会话解析的 manifest generation 不变；每个分片摘要与时间戳对应该 generation；eligible 集合绝不包含未完整校验或契约字段不全的 release。播放器可以在同一 manifest 的 rendition 间切换，但不能用相同序号猜测另一 generation 兼容。服务端记录发布、下架与策略变更，边缘缓存的是不可变对象，不是“当前标题”这个可变事实。

## 必然约束

[DEDUCED:video-on-demand-release-selection-must-bind-a-complete-client-compatible-contract] 假设新版本有 120 个分片却只完成 119 个，或只产出 AV1+DRM-X，而某电视只支持 H.264+DRM-Y。把这个版本设成唯一 current，前者会在播放中 404，后者在首帧前就无法解码或取证。分片上传成功与 release 发布必须分离，protocol/schema、codec、DRM scheme、key generation 和必需资产均完整后才可加入 eligible 集合；网关再按能力选择最新兼容 release，而非向所有客户端返回同一指针。

[DEDUCED:video-on-demand-license-revocation-has-an-explicit-exposure-window] 用户在离线设备取得七天 license、内容密钥和部分加密分片后，服务端第二天撤权。当前 deny generation 可以阻止新的 manifest、segment authorization、license 和 renewal，却不能让设备忘记已经签发的 license、密钥、已解密帧或离线字节。最坏暴露边界由 license/离线租约 expiry 或强制在线回检间隔决定；若产品要求十分钟撤权，就必须把可离线时长压到该上界并承担续期依赖，不能承诺控制面写入后即时远程收回。

[DEDUCED:video-on-demand-startup-and-steady-state-throughput-are-separate-budgets] 即使一小时平均下载带宽高于码率，首个 DNS、授权、manifest 和冷 miss 串行耗时 1.8 秒仍会违背一秒首帧目标；反之首片从边缘 50 毫秒返回，但之后持续吞吐只有所选码率的 80%，缓冲终会耗尽。因而启动 SLI 与 rebuffer SLI 必须分开，不能用平均带宽或总体成功率替代。

## 从简单方案演进

最简单正确基线是单个不可变 MP4、单地域对象存储和下载播放。它适合短片、低并发及网络稳定环境。第一个演进是切成分片并提供一个清单，使播放器可拖动和渐进播放；当完整文件下载造成点击到首帧 `p95` 超过目标的两倍，或用户平均只观看文件前 20% 而仍下载大部分字节时，这一步变优，但新增请求放大和分片完整性问题。

第二步加入多码率对齐转码与 ABR。当卡顿会话比例连续一周超过待校准的 1%，且吞吐分布覆盖至少两个明显档位时，存储 `Σri/r0` 放大换取体验；若用户全在受控局域网、单一设备，单 rendition 重新更优。设备能力开始分叉后再把单 current 演进为兼容 release 集合：新客户端逐步采用新 codec/DRM，旧 release 由活跃设备占比和支持截止期决定何时退役。第三步在地域边缘缓存不可变 segment。当源站峰值出口超过预算的 70%，或热门前 1% 标题贡献 60% 以上字节时迁移；这降低回源，却新增冷 miss 风暴、缓存成本和策略撤销路径。

再往后按内容热度预热首批分片、对长尾按需回源，并把编码任务按标题公平排队。两个门槛都要压测：首三片缓存命中从 90% 降到何值会击穿首帧 SLO；单标题回源并发达到多少会耗尽源站连接池。反选“覆盖同名对象并用短 TTL”是因为播放会混版；若内容永不修改、无审核回滚且客户端总是整文件下载，它才可能重新简单且正确。

## 设计决定

正常路径为：上传源 generation，异步产生对齐 rendition 与摘要，构造冻结协议/schema、codec、DRM/key generation 的 release manifest，验证全部必需资产，再以 CAS 把 release 加入标题 eligible 集合。播放网关读取当前集合、客户端能力与授权策略，选择最新兼容 manifest；需要 DRM 时签发绑定该 manifest、key generation、设备和 expiry 的 license，续期再次检查当前权利。客户端随后从边缘读取不可变分片。请求超时可重试相同分片，摘要使重复字节可验证；发布 CAS 重试携带同一 release id，不会重复注册。

过载按最小到最大用户可见依次降级：停止冷门预热和未来分片预取；减少最高码率可选项；延长启动缓冲并降低并发下载；对新会话排队或按租户公平拒绝。已开始会话和首片优先于后台转码。不得把缺失分片当作空白成功，也不得在不兼容版本间兜底。撤权后网关与边缘阻止新的 manifest、segment authorization、license 与 renewal；已签 license、已取得密钥、已解密内容和离线字节只能等其 expiry/离线租约或设备在线回检，不冒充即时收回。未选择每次播放动态拼装任意 rendition，因为它把发布时可验证的不变量推迟到用户路径；只有个性化广告必须逐会话编排且已有严格时间轴校验时才重新变优。

## 运行与演进

SLI 包括首帧 `p50/p95/p99`、rebuffer 次数与时长占比、平均观看码率、manifest/segment 错误率、能力匹配失败与旧 release fallback 率、首片与后续片命中率、源站 offload、发布耗时、不完整/不兼容发布拒绝数、license 签发/续期失败、撤权后新请求阻断率和未到期离线 license 暴露上界。两组待校准切换指标是：当首片冷 miss `p95` 超过首帧预算的 40% 或源站出口持续十五分钟超过 70%，启动首片预热；当某旧 release 的活跃兼容设备低于待定阈值且 license 最长期限全部结束，才退役其分片和 key generation。百分比须由真实网络分布和业务价值校准。

故障演练：零分钟 H.264 release 7 正常；一分钟 AV1 release 8 的 119/120 片完成；两分钟发布校验失败，8 不进入 eligible 集合；三分钟补片完成后注册 8，新设备选 8，旧电视仍选完整的 7。随后让旧 worker 迟到重写第 61 片，摘要和不可变写拒绝它。四分钟撤销某离线设备权利：新 manifest、segment authorization、license 与 renewal 均拒绝，但此前签发的 license 只在其五分钟测试 expiry 后失效，演练不得把这五分钟记成即时撤权成功。再让某边缘丢失 8 的首片，验证回源请求合并而非十万会话齐冲源站。

安全上，私有内容每次创建会话、签发 license 和续期都检查当前授权和地域策略，短令牌与短 license 限制已发授权暴露；紧急下架提升 deny generation，使边缘在授权新的分片读取前校验，不把长 TTL 当授权。离线播放的产品承诺必须显式接受在 expiry/回检前不可撤回的窗口。多租户限制转码、回源和出口份额。内容擦除先禁止新会话，再清理不可变版本并收集副本确认；控制面接受删除不冒充全球物理擦除完成。

## 面试考察本质

这题的本质是：给定“一次播放只能观察一个完整且与客户端能力兼容的不可变时间轴”，由于分片处理、设备能力、缓存传播和网络吞吐都不能瞬时获知且带宽稀缺，候选人应推导出发布与传输分离、兼容 release 选择、startup 与 rebuffer 分账，以及离线授权撤权窗口；再按内容价值、网络分布和下架风险选择版本、梯度与缓存策略。

优秀信号是先定义 manifest/segment generation 和原子发布，再算 `H×Σri` 与请求放大，构造 119/120 片和旧电视不兼容反例，并给出 license 到期前的信息暴露边界。常见误区是列播放器、对象存储和 CDN 后便结束，用平均带宽代表体验，把单个 current 推给所有设备，或声称撤权能删除已发密钥和离线字节。追问可落到拖动、字幕同版、首片热点、编码/DRM 迁移、私有内容撤权和源站故障。

二十分钟应完成 SLO、容量式、不变量和发布路径；四十分钟加入 ABR、缓存、过载与故障时间线；六十分钟再讨论多地域授权、编码迁移、成本归因和擦除。回答质量不取决于组件名，而取决于能否指出哪个版本拥有事实、何时可见、失败后用户究竟继续看旧完整版本还是得到明确失败。
