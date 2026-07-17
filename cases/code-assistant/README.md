# 代码助手

## 表面题目

设计代码助手，表面上是开发者在编辑器中提问、补全、生成补丁或请求解释，系统读取仓库上下文并返回建议。真正成功包含四层：只读取当前身份获准访问的文件；上下文对应明确仓库修订；建议能应用于用户当前工作树；验证结果说明它是否编译、测试或违反策略。模型输出文本成功不等于代码变更成功，更不等于有权提交。

题名容易忽略本地未提交修改、分支快速推进、生成代码中的 secret 与许可证风险，以及大型仓库远超上下文窗口。本设计覆盖仓库问答、行内补全和候选 patch，不替代代码评审、CI 的最终权威，也不允许服务直接绕过分支保护合入主线。

## 反问与边界

先明确产品模式：只读问答、单文件补全、跨文件重构还是能发起 PR；延迟目标是补全的首候选百毫秒级还是异步重构分钟级。质量不能只看“看起来正确”，至少要定义 edit acceptance、编译率、目标测试通过率、缺陷/安全回归率和对照任务成功率，同时观察首候选延迟、完整 patch 延迟与每个被接受变更的推理和验证成本。

仓库规模、语言、单体/多仓、符号图更新时间、并发 revision、未提交工作树大小和生成峰值决定容量。权限要问到路径、分支、子模块、历史 commit 与 secret；被删除文件和撤销权限是否必须从 embedding、日志、提示缓存与评测样本清除。还要确定模型/分词器/系统提示、索引、构建镜像、工具版本和采样参数是否可追踪。非目标是保证概率生成逐字相同，或把测试通过等同于业务正确。

## 客观模型

最小接口为 `BuildContext(repo_id, base_revision, workspace_digest, principal, task)`、`Generate(context_manifest, intent, request_id)`、`Validate(patch_digest, base_revision, workspace_digest, toolchain_digest)`、`ApplyLocal(patch, expected_head, expected_workspace_digest)` 与 `PublishBranch(patch, expected_ref, permission_epoch)`。context manifest 为每个获准路径区分 `(remote_preimage_digest, workspace_effective_digest, permission_epoch)`；patch artifact 绑定 `base_revision`、完整 `workspace_digest` 和所有 touched/read path 的两层摘要，使本地 delta 可从远端 preimage 完整重建。远端发布器拥有 expected_ref 所指树与 candidate tree 构造权，权限服务拥有当前 path/branch policy epoch，ref store 只拥有最后的引用 CAS。

设仓库 token 总量为 `R`，上下文预算为 `B` 且 `R≫B`。选择器的目标不是截取前 `B` 个 token，而是在预算内最大化与任务有关的定义、调用者、测试和约束覆盖。单请求成本近似为 `C=C_index_lookup+C_model(B,O)+ΣC_validator`，跨文件候选数 `m`、测试分片数 `s` 会把验证放大到 `m*s`。热门基础库、生成的大文件和少数超大 diff 形成明显偏斜。

不变量包括：上下文中的每个字节可回到指定 repo revision 与当前允许的路径；本地 patch 只能对同时匹配 expected_head、workspace_digest、逐文件 preimage 和当前权限的工作树原子应用。远端发布先用 `remote_preimage_digest` 匹配 expected_ref 树的所有 touched/read path，再把完整本地 dependency delta 与 patch 确定性合成 candidate tree；无法表达任一路径从远端到本地有效 blob 的变化就拒绝。最终发布事务复查路径权限、branch protection 与 permission_epoch，并以 `expected_ref → candidate_tree` CAS 线性化。ref CAS 只防并发推进，不证明 patch 适用。

## 必然约束

[DEDUCED:code-assistant-context-must-bind-revision-and-permission] 代码上下文必须同时绑定仓库修订与当前路径权限，缓存命中不能证明旧内容在本次请求中仍可披露。反例是用户在 revision A 时可读 `secrets/client.go`，上下文缓存已保存它；管理员撤权并推进 permission_epoch 后，用户在 revision B 请求解释。若缓存键只有 repo 和 query，相同命中会泄露旧文件。故缓存至少按 blob digest 与授权世代校验，发送模型前还需当前权限栅栏；撤权删除要覆盖派生索引。

[DEDUCED:code-assistant-generation-is-not-a-commit-authority] 模型生成补丁只产生候选意图，写入当前工作树或远端分支前仍需基于目标树、逐文件 preimage、当前权限与保护规则做适用性检查。反例是助手基于 head 100 加本地未提交依赖 W7 生成 P，远端 ref 仍恰好是 100；只做 expected_ref CAS 会成功，但远端树没有 W7，P 编译失败。发布器必须把 W7 中被 P 依赖的变化作为完整、可验证的 candidate tree 输入，或拒绝远端发布；即使 ref 未变，permission_epoch 或 branch protection 变化也必须拒绝。CAS 通过不能把生成时间的正确性延伸到远端树。

[DEDUCED:code-assistant-context-budget-requires-structural-selection] 仓库通常大于上下文窗口，按文件顺序截断无法稳定保留定义、调用关系与失败约束，选择必须利用结构和任务目标。一个 10 万 token 仓库只允许 8 千 token 上下文时，目标函数定义在末尾、测试在另一模块；按字母序装入前 8 千会同时漏掉二者。增加窗口可降低漏选概率，却线性增加预填充延迟和费用，并让无关内容竞争注意力，因此必须度量 context recall，而不是把最大窗口当唯一答案。

## 从简单方案演进

基线是只发送当前打开文件及用户选区，助手返回不自动应用的 patch。它权限边界直观、延迟低，适用于局部补全。当离线任务中因缺少跨文件定义导致的失败超过总失败的百分之十，或用户手工追加上下文比例连续一周高于百分之二十，才引入符号图和仓库检索。这两个阈值是待离线标注与产品数据校准的初始值；调低会提高上下文成本和泄露面，调高会容忍更多无效建议。

第二步构建 revision-aware 索引，按任务扩展定义、引用、测试与构建文件，并让权限过滤进入候选选择。若索引 lag 的 `p99` 超过两个 commit 或十分钟，查询回退到当前 revision 的直接文本搜索，避免静默使用旧符号图。长异步任务再加入候选验证沙箱；当验证排队 `p95` 超过交互 SLO 的一半时，区分快速语法/类型检查和完整测试，后者异步返回。具体 commit、分钟和比例都需通过仓库提交节奏与压测校准。

第三步支持跨文件 patch 与 PR，但本地应用走 workspace/preimage 原子校验，远端发布走“目标树 preimage 与策略复查—完整 candidate tree 构造验证—ref CAS”三段式协议。若每个被接受建议的验证 GPU/CPU 成本超过人工节省，缩小候选数或只对高风险变更跑完整测试；若安全误报造成接受率下降，则按语言与路径校准，而不关闭 secret 扫描。

未选“上传整个仓库给超长上下文模型”，因为成本、权限撤销和 revision 归因不可控；在小型公开仓库、上下文可完整容纳时它重新变优。也未选模型直接写主分支；只有一次性沙箱、自动生成仓且有独立发布门禁时，自动应用才可能比人工确认更优。

## 设计决定

请求先固定 `(repo_id, base_revision, workspace_digest, principal, permission_epoch)`。选择器从 revision 与本地工作树构造 context manifest；每个片段同时保存远端 base blob、模型实际读取的 workspace blob、路径与选择理由。对本地未提交内容只使用会话加密存储。生成 attempt 固定模型、提示、索引和工具版本；结构化 patch artifact 为每个读取、修改、删除或新增路径保存 `remote_preimage → workspace_effective → candidate_output` 链，新路径的远端 preimage 使用“必须不存在”哨兵，因而 dependency closure 可独立于原工作树重建。

验证按风险分层：格式/解析、类型与定向测试先跑，依赖与安全扫描随后。本地接受在一个原子事务内复查 HEAD、workspace、所有 preimage 与当前写权限。远端发布器读取 expected_ref 的不可变树，逐一比较所有 touched/read path 的 `remote_preimage_digest`；若 P 依赖 W7，本地 delta 的全部 blob、来源 remote preimage、权限和验证结果必须进入 dependency closure，否则拒绝。发布器按路径稳定顺序先应用 closure、再应用 P，得到 content-addressed candidate tree 并重跑门禁；最终仓库事务在同一点复查 expected_ref、当前 permission_epoch、路径写权限与 branch-protection policy digest，再把 ref CAS 到 candidate commit/tree，避免策略检查后的竞态。

过载先停用昂贵的多候选与全仓测试，再降为当前文件补全，最后拒绝异步重构；任何层级都不降低权限或 secret 检查。模型、索引和构建基础设施即使完全版本化，概率候选仍可能不同，因而重放承诺是“相同输入与环境可分析”，不是逐字符相同。

## 运行与演进

SLI 以接受后结果为中心：上下文定义/测试召回率、候选接受率、apply 成功率、编译与目标测试通过率、合入后回滚/缺陷率；体验看首候选、完整 patch、验证和取消释放延迟；成本看每个被接受 patch 的输入输出 token、索引 CPU 与沙箱分钟。安全监控跨权限候选、secret 阻断、撤权残留和未提交代码保留时间，并按 repo/model/index/toolchain generation 分桶。

故障时间线：t0 用户基于 head 100、workspace W7 建 context，W7 新增 helper H；t1 助手生成调用 H 的 patch P；t2 远端 ref 仍为 100。若只做 ref CAS，缺少 H 的树会被发布；正确流程要么把 H 及其 preimage/权限纳入 candidate tree 并验证，要么拒绝。另一支演练让 permission_epoch 从 8 变 9 或 branch protection 新增必需检查，即使 ref 仍为 100、所有 blob 未变，策略复查也拒绝旧授权；若 ref 变 101，最后 CAS 再防并发推进。三个门禁分别证明适用、授权和线性化。

每次 release 绑定不可变 eval manifest：脱敏 repo/task fixture digest、期望测试与可接受 patch 标签 digest、人工正确性/可维护性 rubric 或 judge version、context-recall/编译/安全指标实现版本。升级只在同一 eval manifest 上比较 context recall、目标测试通过、secret 漏检、延迟与单位接受成本；数据或 rubric 变化先建立新基线，不能与旧分数直接宣称提升。灰度按 repo 固定版本谱系，回滚也引用同一 manifest。质量下降超过业务容忍或安全漏检非零即停止灰度；接受成本超过节省的评审分钟则降候选数。

## 面试考察本质

给定“助手只能基于获授权的确定仓库状态提出候选，且本地或远端 candidate tree 未经当前树 preimage、权限与保护规则验证不得成为提交”这一不变量，因为仓库远大于上下文、本地依赖未必存在于远端、revision 与 permission_epoch 都会变化，候选人应推导出绑定 revision/workspace 的上下文、dependency-closed candidate tree、发布前门禁和最终 ref CAS。CAS 只线性化已验证树，不能替代适用性证明。

优秀回答会区分生成、验证、应用三种状态所有者，讨论本地修改与远端 revision，量化上下文召回和每个接受建议成本，并明确 secret 删除传播。常见误区是“把仓库向量化”后不谈权限世代、把测试通过当形式证明、模型直接 push、或认为固定 temperature 即可稳定重现。

二十分钟给出 context manifest、patch 状态机、`R≫B` 预算和 stale-head 反例；四十分钟加入本地 preimage、远端 dependency closure、权限/branch protection 与沙箱验证；六十分钟讨论灰度评测、撤权清除、提示注入和成本归因。追问用“远端仍是 100，但 patch 依赖未提交 W7”检验候选树完整性，用“ref 未变而权限已撤销”检验策略世代，再用并发推进到 101 检验 CAS 仅负责最终线性化。
