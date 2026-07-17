# 图片服务

## 表面题目

设计图片服务，是让用户上传一个源对象，按裁剪、缩放、旋转、格式和质量请求派生图片，并以稳定 URL 低延迟返回。成功需要区分源字节已完整持久化、配方已被接受、variant 已确定性生成、当前访问者有权读取。系统不是普通文件缓存：同一源可能产生上百种组合，处理器升级可能改变输出字节，EXIF 方向和色彩配置也会改变视觉结果。

本文把源图片建模为不可变 `source_digest`，处理配方经过规范化并带执行器版本，variant 同样不可变。可变的是逻辑图片指向哪个 source generation，以及访问策略 generation。任意尺寸不是无限承诺；服务通过白名单或成本预算限制像素、格式与组合，防止攻击者制造无界计算和缓存键。

## 反问与边界

先问源文件大小和像素分布、读写比、热门度、允许的变换集合、首读与缓存命中 SLO、上传后多久可见、是否私有、多租户和删除要求。裁剪是用户显式焦点还是自动识别，动画图是否保留帧，透明通道和色彩空间如何处理，都必须进入字节契约。还要确认相同 URL 是否必须永久返回同字节，处理器升级如何灰度，未知 recipe 是拒绝还是异步排队。

设源数 `N`、每源平均被访问的有效配方数 `v`、平均 variant 大小 `b`，派生存储约 `N×v×b`；若允许宽、高各一千种再乘五种格式，理论组合达五百万/源，不能全预生成。处理成本可近似与解码像素 `Psrc`、变换像素和编码像素 `Pout` 成正比，而非输出字节数。两万像素方图约四亿像素，即使压缩文件只有几十 MB，也会制造显存/内存尖峰，必须在解码前校验上限。

## 客观模型

实体是 `LogicalImage`、`SourceGeneration(digest,metadata)`、`NormalizedRecipe(width,height,fit,format,quality,metadata_policy,encoder_version)`、`VariantKey`、`BuildAttempt(attempt_generation,lease,fence_token)` 和 `PolicyGeneration`。`VariantKey = H(source_digest || canonical_recipe || processor_build || color_policy)`。上传先流式计算摘要、验证声明格式与解码限制，再提交不可变 source；读取网关认证后把请求规范化，查已生成 variant，miss 时由权威协调状态为该 key CAS 创建一个当前 BuildAttempt，而进程内请求合并只负责减少等待者，不拥有发布权。

同一键的状态只有 absent、`generating(attempt_generation,lease,fence_token)`、ready(digest) 或 failed-retryable。每个 attempt 只写 `variant_key/attempt_generation/fence_token` 下自己的不可变临时对象；租约过期后协调器 CAS 提升 generation 与 token，只有仍等于当前 token 的提交才能把 generating CAS 为 ready(digest)。旧 attempt 即使比新 attempt 先完成、甚至算出相同 digest，也没有发布或复用资格；复用已有 digest 仍须由当前协调状态确认输入与 token 后提交，digest 相等不能替代所有权。临时对象绝不对读者可见。

若单个源每天收到 `q` 次读取、命中率 `h`，生成请求约 `q×(1-h)`，但热点并发 miss 若无合并可瞬间放大为 `m` 次相同解码。惰性生成的首读延迟下界包含源读取、完整解码、变换、编码和 variant 写入；预生成则把这笔成本放在上传路径并为无人访问组合付费。

事实边界是 source digest 与 recipe 的联合版本。逻辑图片换源生成新 digest，旧 variant 不覆盖；处理器升级进入新 key，两版可并存并灰度。访问策略不应成为公开字节键的一部分导致无限复制，而是命中后用当前 policy generation 决定是否可返回，或签发短期授权。

## 必然约束

[DEDUCED:image-service-variant-key-must-cover-every-byte-determining-input] 反例：同一源和 `width=200` 在编码器 v1 输出 12 KB，v2 改变色彩与质量后输出 10 KB；若 key 只有路径和宽度，两个边缘会在同一键缓存不同字节，摘要、ETag 与回滚均失去含义。EXIF 旋转、裁剪焦点、质量和 metadata strip 规则同理。所有决定输出的输入必须进入规范化配方或明确冻结，展示参数顺序不同但语义相同则应归一为一键。

[DEDUCED:image-service-build-attempt-fencing-must-gate-publication] attempt 1 的租约过期后，协调器发出 token 42 的 attempt 2；即使 token 41 的 attempt 1 先算完，或两者恰好得到相同 digest，接受它仍会让已撤销的 worker 绕过当前输入、取消和资源所有权。故临时对象必须 attempt 专属，只有当前 token 能 CAS `generating→ready(digest)`；按 digest 复用也必须经过同一当前状态确认。

[DEDUCED:image-service-cached-bytes-must-not-outlive-access-authority] 私有图片在边缘缓存一小时，用户在第十分钟撤销共享。若边缘只凭旧 URL 命中而不校验授权，剩余五十分钟仍可见。字节不可变不代表访问资格不可变；私有读取必须绑定短期令牌或验证当前 deny/policy generation，紧急撤权优先于 stale 可用性。已下载字节无法远程收回，应在契约中明确。

## 从简单方案演进

最简单基线是上传时生成固定的缩略图和大图，写入对象存储。尺寸少、读取稳定时最容易验证。第一切换条件是有效 recipe 种类 `p95` 超过白名单数量两倍，或预生成中连续三十天无人访问的字节超过 70%；此时保留热门预生成、长尾转惰性生成，并用请求合并减少同时等待、用持久 BuildAttempt fencing 控制发布。阈值需按存储价格和冷读价值校准，新方案增加首读超时与失败缓存。

当同一 variant 并发 miss 使重复处理 CPU 超过总处理的 10%，引入按 variant key 的请求合并和持久 BuildAttempt 协调；前者减少同时等待，后者在超时重派后决定唯一发布者。当生成队列年龄超过首读 SLO 的一半，交互请求与后台预热分队列、按租户公平调度。若任意 recipe 带来组合爆炸，则改为签名白名单配方或按像素成本收费。第二组待压测指标是解码像素/秒与内存高水位在何处使 `p99` 急剧上升，以及失败结果缓存多久既抑制重试风暴又不拖延恢复。

边缘缓存只存不可变 variant，逻辑 URL 先重定向或解析到 generation。未选择覆盖 `/image/123/200.jpg` 并 purge，因为升级和回滚期间会发生同键异字节；若图片从不修改、只有一个处理器版本且缓存局限单进程，它才重新可行。也未选择预生成笛卡尔积，只有配方集合很小且每个都高频访问时更优。

## 设计决定

上传生成 source digest 和不可变 generation；读请求先做身份与当前策略校验，再规范化 recipe、验证像素成本，计算 variant key。ready 直接返回；absent 由协调器 CAS 创建带 lease/token 的当前 BuildAttempt，其余请求等待、返回可重试或降级到较小已存在 variant。worker 从源摘要读取、在沙箱内解码，只写 attempt 专属不可变临时对象；校验输出尺寸/格式/digest 后携带 token CAS `generating→ready`。租约超时会提升 attempt generation，旧 token 不能发布；即使对象仓中已有相同 digest，复用也要由当前 token 完成状态提交。处理器升级进入新 key，不靠覆盖旧结果。

过载按用户影响递增：暂停后台预热；延后冷门新 recipe；降低质量或返回同纵横比的较小已发布 variant；对高成本变换排队；最后拒绝新生成但仍服务已有 variant。绝不返回错租户、错裁剪或部分编码字节。反选把处理直接放在每个 CDN 边缘，因为版本、沙箱和计算容量难一致；当变换极轻、边缘执行环境可证明同版本且源读取成本主导时，它可重新变优。

## 运行与演进

观察源/variant 完整性、生成 `p50/p99`、请求合并率、BuildAttempt 租约过期/重派、fence 拒绝、旧 attempt 同 digest 复用拒绝、缓存命中、解码像素率、内存峰值、失败配方、存储放大 `Σvariant/source`、策略拒绝和撤权后命中阻断。待业务校准：上传时预生成应覆盖多少访问百分位；当某处理器版本视觉差异、错误率或字节放大超过何值回滚。按租户展示成本，避免平均命中掩盖恶意 recipe。

故障演练：0 分钟 source digest S 发布；1 分钟一百个请求同时要 recipe R，协调器创建 attempt 1/token 41，所有请求合并等待；2 分钟其租约过期并 CAS 创建 attempt 2/token 42，两者各写自己的不可变临时对象；2.5 分钟 attempt 1 反而先得到 digest V，但 token 41 被拒，读者仍不见 ready；3 分钟 attempt 2 可复用相同 V，也必须携带 token 42 确认当前输入并 CAS ready。随后撤销访问 policy 9 升到 10，边缘已有 V 也必须拒绝旧令牌。处理器升级采用双写影子 key，对尺寸、摘要稳定性和视觉抽样比较，灰度切逻辑生成器版本；回滚只切回旧 key。

安全边界包括解压炸弹、畸形解析器、SVG 外部资源、元数据泄露与租户路径穿越。worker 无网络、有限 CPU/内存，源和 variant key 都带租户域。删除先拒绝新读，再清理 source 和派生索引并追踪副本；哈希去重若跨租户会泄露存在性，应限定信任域或加租户盐。

## 面试考察本质

本题本质是：给定“相同源摘要和规范化 recipe 必须得到一个可验证的不可变 variant”，因为访问分布、未来尺寸需求和处理器输出变化不可预知，而像素计算与派生存储稀缺，候选人应推导完整 variant key、按热度选择预生成/惰性生成，并把字节缓存与可撤销授权分开。

优秀回答会用组合数量说明为何不能全预生成，指出压缩字节小不等于解码便宜，构造编码器升级同键异字节与旧 attempt 先完成的反例，并描述“当前 fence token + attempt 专属对象”到 ready 的原子边界。常见误区是只谈对象存储/CDN、把进程内 single-flight 当跨重派写权限、让 query 参数无界进入处理器、缓存部分文件，或把 URL 难猜当权限。

二十分钟完成 API、key、成本式和冷热策略；四十分钟加入请求合并、BuildAttempt fencing、沙箱、授权与故障；六十分钟讨论版本迁移、跨租户去重、视觉回归和删除。换成另一媒体题后仍成立的缓存套话不够，候选人必须说清哪些配方字段决定像素、哪个 digest 是事实、以及冷 miss 到底让谁承担计算。
