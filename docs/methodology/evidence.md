# 证据方法论

Works on My Whiteboard 不把“图画得像”当作系统设计结论。每条结论都必须说明它是什么类型的主张、由哪些输入约束、如何运行、选中了哪份不可变记录，以及还不能说明什么。本页定义 W0 的证据边界；它不是生产容量认证，也不是完整发布声明。

## 内容可用不等于证据有效

题目正文与证据闭环是两个独立口径。完整的第一性原理文章说明该题已经给出目标边界、客观模型、不变量、演进条件和设计取舍，适合阅读与面试练习；它不等于结论已经在生产环境中测量，也不等于实验、来源和发布依赖已经闭合。`status: draft` 描述的是后一条证据生命周期，不表示正文缺失。

当前证据快照只认证 `distributed-rate-limiter` 纵向金样板及其依赖的令牌桶实验。其余案例中的 `DEDUCED` 主张仍须按各自前提和适用边界阅读，不能从“题目内容覆盖：75/75”推导出生产测量有效或“实验与证据闭环：75/75”。严格闭环进度继续由 [`generated/coverage.md`](../../generated/coverage.md) 报告为 **1/75**。

## 四类主张

每条主张必须且只能属于以下一类：

- `ASSUMED`：题目显式给出的前提，或为了继续推导而公开声明的边界。它需要可见，但不能伪装成事实。
- `DEDUCED`：从已声明前提、数学关系和系统不变量推导出的结论。推导链必须能够回到输入。
- `MEASURED`：由本仓库中可重复执行的实验支持的结论。它必须绑定运行参数、环境、判定条件和局限。
- `SOURCED`：由可定位的外部材料直接支持的结论。它必须保留来源标识和 HTTPS 定位信息。

分类描述的是“为什么可以提出这条主张”，运行状态描述的是“某次实验发生了什么”；两者不能互相替代。

## 身份模型：主张、绑定、单元格与运行

身份从稳定定义到一次尝试逐层收窄：

1. `claim_id` 标识一条稳定主张。
2. `binding_id` 把一条主张绑定到一个 workload 和一组预先声明的 assertions；一次实验记录只能服务一个 binding、一个 claim。
3. 一个矩阵单元格由六个字段共同标识：`lab_id`、`required_run_id`、`binding_id`、`claim_id`、`implementation_id`、`adapter_id`。baseline、variant、adapter 的 role 单独记录，不能代替这六字段。
4. 每次尝试有唯一 attempt ID；同一次当前调用产生的全部尝试共享唯一 `run_set_id`。
5. release 选择项同时固定单元格、attempt ID 和内容摘要，因此“同名实验”不能替换已经审计的字节。

这套模型执行“一次运行、一个 binding、一个 claim”的约束，也能区分同一实现经不同 adapter 执行的结果。

## 可复现执行边界

- 实验使用固定的逻辑时间轴；事件时间来自规范，不读取墙上时钟来决定正确性。
- 每个定义使用固定 seed。随机扰动必须由该 seed 派生，并记录在证据中。
- 正确性 metric 使用带显式 unit 的整数，例如 requests、count 或 tokens；不以浮点误差或跨机器绝对吞吐阈值决定通过。
- workload 参数、fault 注入时刻、deadline、预期事件数、measurement unit 和 assertions 在运行前冻结。runner 只能消费副本，不能回写发布权威定义。
- `smoke` 和 `deep` 是显式 profile。profile 是证据身份的一部分，不能用一个 profile 的历史记录填补另一个 profile 的当前单元格。

因此，字节稳定表示逻辑输入和结论稳定，不表示不同 CPU、操作系统或调度器会产生相同的性能数值。

## Git 输入摘要与构建来源

`input digest` 从仓库精确 Git 顶层的 **Git index** 计算：按索引中的 path、Git mode 和 blob bytes 做有边界的哈希，同时确认 HEAD、index 与工作树在读取前后都没有变化。输入摘要不是对当前目录随手打包，也不接受 sparse、skip-worktree、assume-unchanged、符号链接替换或未声明的脏文件。

排除项是窄白名单，只覆盖规范命名的 `evidence/runs` 记录、`evidence/releases` manifest、`generated/coverage.md` 和本地验证缓存等产物。源代码、定义、脚本及其可执行 mode 都属于输入；未知 evidence/generated 路径会被拒绝。这样 source commit A 之后生成证据，不会反过来改变 A 的输入身份。

可执行文件还必须带有 Go build provenance：VCS 类型为 Git、嵌入 revision 等于当前 HEAD，并且 `vcs.modified=false`。运行前后再次计算 source state；源码在执行中变化时，当前结果不能发布。仅知道一个 commit 字符串不足以绕过这些检查。

## 当前调用、只追加记录与快照

一次 `run --required --snapshot` 先解析当前六单元格闭包，再分配新的 `run_set_id`，逐一执行并只追加 canonical JSON 记录。证据文件名由 attempt ID 决定，内容带自校验 digest；已有名字绝不覆盖，即使新字节完全相同。失败、跳过或不可判定的尝试可以作为事实保存，但不能被选择成通过的正式快照。

release 构建只允许选择**当前调用**刚产生、同一 run set、同一 input digest、全部 `passed` 的完整矩阵。历史记录不能填补本次缺失单元格。发布结果是按 input digest 定位的**不可变 release snapshot**：manifest 固定 profile、run set、六字段单元格身份、attempt ID 和内容摘要；第二次写入只能得到冲突，不能替换赢家。

审计会重新加载 manifest 和每条记录，验证 canonical bytes、内容摘要、当前定义闭包、单元格唯一性、run set、source state 与选择完整性。报告只能从完整审计后的绑定值生成，不能从目录中“挑看起来最好”的结果拼接。

## 运行状态

- `passed`：本次运行完成，所有预先声明的 assertions 都满足。
- `failed`：运行完成或中止，但至少一个判定条件明确不满足。
- `skipped`：由于记录下来的前置条件，本次实验没有执行。
- `flaky`：相同逻辑输入的重复尝试呈现不稳定结果；它不是“偶尔算通过”。
- `inconclusive`：执行产生了信息，但不足以支持通过或失败结论。

这些状态不自动提升主张强度。例如 `passed` 的本地 `MEASURED` 证据仍受本地环境限制。

## A/B 两提交证据流程

为了同时满足干净构建来源与不可变证据，W0 使用两个提交：

1. **source commit A** 只包含源码、定义、CI、文档和验证脚本。
2. 从 A 创建独立 checkout：`git clone --no-local --no-checkout ...`，detached checkout 到 A，构建 revision=A 且 `vcs.modified=false` 的二进制。
3. 该 A 二进制运行六单元格、生成并审计记录与 manifest；证据出现后继续复用同一个干净 A 二进制，不能因工作树新增证据而重建成 modified binary。
4. 只把规范的记录和 manifest 复制回功能分支，形成仅含证据的 **evidence commit B**。A 与 B 的 `input digest` 相同，每条记录的 `source_commit` 仍是 A。
5. 再创建一个独立 `--no-local` clone，detached checkout 到 B，构建干净 B 二进制；它审计由 A 生成、但输入摘要未变化的证据。

这里明确不用 `git worktree` 生成权威证据：linked worktree 的 `.git` 是文件，无法作为 Go 1.26.5 构建来源盖章的可靠前提。两个独立 clone 也防止本地主仓库的 index、replace refs 或未提交状态污染 A/B 结论。

## 本地环境与结论边界

W0 的实验是在单进程、本地环境中执行的确定性模型和场景。它验证令牌守恒、边界时刻、全局配额放大和 outage policy 等有限主张；它没有验证真实网络、跨区域时钟、长期数据保留、生产故障域、尾延迟或生产容量。deep 报告中的性能差异只用于同次运行的相对观察，跨机器 diff 是信息，不是绝对门槛。

所以本仓库不会从六个 `passed` 单元格推导“可直接生产部署”，也不会据此宣称 75 题都已经得到实验认证。

## 为什么开发验证可以通过，而正式发布仍是 expected-negative

开发模式检查当前已声明内容的结构、闭包和可执行证据；W0 的 `distributed-rate-limiter` 纵切面可以完整通过这些门。正式 release validation 还要对冻结的 75 题 baseline 做严格的实验与证据闭环审计，而不是统计正文是否存在。当前闭环覆盖严格为 `1/75`，唯一发布诊断是 `release_scope_incomplete`，汇总为 `complete=1 baseline=75 missing=74`，并列出精确排序的 74 个 ID。

因此 `make verify-fast`、`make verify-deep` 和证据审计可以通过，而 `make verify` 在 W0 必须非零。`scripts/assert-w0-release-contract.sh` 只有在 release 命令以约定退出码返回、且结构化结果恰好是这一条 expected-negative 诊断时才返回成功。它证明“阻塞原因准确”，不把被阻塞的正式发布改写成绿色发布。
