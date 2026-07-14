# W0 Task 4 报告 — Content Validation and Catalog CLI

## 状态

DONE。实现基于 `95ac3e255e2a4030875d2fa6149bf040bf7ab0d2`，提交信息固定为 `feat: add validation and coverage CLI`，未执行 push。

## 范围

本任务只新增内容校验、`validate` / `coverage` CLI 与薄入口：

- `internal/content/headings.go`、`headings_test.go`
- `internal/content/tags.go`、`tags_test.go`
- `internal/content/links.go`、`links_test.go`
- `internal/content/validate.go`、`validate_test.go`
- `internal/cli/app.go`、`app_test.go`
- `internal/cli/validate.go`、`validate_test.go`
- `internal/cli/coverage.go`、`coverage_test.go`
- `cmd/whiteboard/main.go`

没有修改 catalog/validator、规范或计划，没有新增 harness、evidence 或真实 case/principle 内容。

## 实现摘要

### Markdown 内容契约

- 使用 Goldmark AST（启用 table extension），不以正则解析 Markdown。
- complete case 精确校验 8 个直属 H2 的名称、唯一性和顺序；complete principle 对应校验 6 个直属 H2。
- 每节必须有非空 paragraph/list/table/indented code/fenced code；H3 与纯 HTML 不计为正文。
- 对排除 code、HTML、link destination 后的可见 prose 做 Unicode whitespace collapse，并按 Unicode rune 计数；阈值完全冻结为 brief 指定值。
- 在可见内容中拒绝 `TODO`、`TBD`、`FIXME`、`XXX`、`待补充`、`待完善`；代码、HTML comment、link destination 被忽略。
- 支持且只支持四种精确 claim marker：`ASSUMED`、`DEDUCED`、`MEASURED`、`SOURCED`；拒绝 malformed、未知 claim、缺 marker 与同 claim 冲突 class。
- `ASSUMED` 的同一内容块必须同时包含 `原因：` 与 `变化影响：`。
- `MEASURED` 必须经 owner evidence requirement 到达正确 lab kind、唯一精确 owner/claim binding，并被 required run 引用。
- `SOURCED` 必须解析为 catalog 中存在、且被 owner manifest 列出的 source；alias resolution 同样生效。
- draft 内容不提前施加 complete prose contract；所有已编写 Markdown 的内部链接仍由仓库级链接检查覆盖。

### 仓库链接契约

- 确定性发现仓库内 `.md`，跳过 `.git`、`.superpowers`、`evidence`、`generated`。
- 校验相对目标存在、大小写精确、不能越出 root、symlink 最终仍在 root 内，且目标必须是普通文件或目录。
- 目录 fragment 解析到 `README.md`；非 Markdown 目标不校验 heading fragment。
- Markdown fragment 使用仓库冻结的 Unicode slug 与重复 heading 后缀规则；目标解码一次，不接受反斜杠、NUL 或绝对仓库路径。
- complete manifest 缺少规范 `cases/<id>/README.md` 或 `principles/<id>/README.md` 时给出稳定诊断。

### CLI

- 暴露 `Run(ctx, args, stdout, stderr) int`；使用 `flag.FlagSet` + `ContinueOnError`，parser 输出定向到 stderr。
- `validate` 只加载 catalog 一次，并总是运行 semantic validation；只有 `--content` 或 release 才增加内容/链接校验，release 强制启用内容校验。
- `--release` 仅接受 `current` 或 `sha256:<64 lowercase hex>`；诊断排序后以 text 或 two-space-indented JSON 输出并以单个换行结尾。
- `coverage` 支持 text/json/markdown；stdout/output 的 bytes 完全一致且确定性生成。
- `--output` 使用同目录临时文件、短写检查、close、`0644` 与 rename；不会隐式创建父目录。
- `--check` 只读现有 output 并逐字节比较，绝不写文件。
- 稳定退出码为 success 0、argument/load 2、development validation 3、release/audit 4、lab execution 5。
- `cmd/whiteboard/main.go` 只调用 `os.Exit(cli.Run(...))`，没有业务逻辑。

## TDD RED / GREEN 证据

### Cycle 1：内容校验 API

先写内容测试，生产 API 尚不存在。

```text
$ go test ./internal/content
exit 1
internal/content/headings_test.go:...: undefined: Result
internal/content/headings_test.go:...: undefined: ValidateCase
internal/content/links_test.go:...: undefined: ValidateRepository
FAIL github.com/PinoHouse/works-on-my-whiteboard/internal/content [build failed]
```

加入最小 AST 实现后，同包测试转绿：

```text
$ go test ./internal/content
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 1.096s
```

### Cycle 2：CLI API 与行为

先写 in-process `bytes.Buffer` 测试，`Run` 尚不存在。

```text
$ go test ./internal/cli -run 'TestValidateCommand|TestCoverageCommand'
exit 1
internal/cli/app_test.go:45:14: undefined: Run
FAIL github.com/PinoHouse/works-on-my-whiteboard/internal/cli [build failed]
```

实现 CLI composition 后，同一 focused 命令转绿：

```text
$ go test ./internal/cli -run 'TestValidateCommand|TestCoverageCommand'
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 1.008s
```

### 对抗回归 RED / GREEN

实现阶段继续以小循环锁定边界：

- lowercase/uppercase 保留 claim class 候选最初漏报 malformed，同时普通 `RFC` / `ADR` 文本被误判；测试先 RED，再限定为四个保留 class 后 GREEN。
- 稳定 I/O path、非 Markdown fragment、普通未闭合 `[` 后的 marker、coverage writer failure 五项边界测试先 RED；移除平台错误文本、只对 Markdown 校验 fragment、继续扫描嵌套 marker、传播输出错误后 GREEN。
- `[DEDUCEDLY:note]` / `[ASSUMEDNESS]` 前缀最初被错误视作保留类；回归测试先 RED，加入 class 后必须为 colon、Unicode whitespace 或结束的边界后 GREEN。

最终 focused 回归：

```text
$ go test ./internal/content -run 'TestClaimCandidateDetection|TestOrdinary|TestValidateRepository|TestValidateCase' -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 1.244s
```

## 最终验证

### 格式、单包与全仓

```text
$ gofmt -d internal/content internal/cli cmd/whiteboard
exit 0（无输出）

$ go test ./internal/content -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 0.952s

$ go test ./internal/cli -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 1.692s

$ go test ./internal/content ./internal/cli ./cmd/whiteboard -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 2.326s
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 1.999s
?  github.com/PinoHouse/works-on-my-whiteboard/cmd/whiteboard [no test files]

$ go test ./... -count=1
exit 0
catalog 0.982s; cli 1.455s; content 1.200s; validator 1.706s

$ go test -race ./... -count=1
exit 0
catalog 3.493s; cli 3.837s; content 5.010s; validator 4.328s

$ go vet ./...
exit 0（无输出）

$ git diff --check
exit 0（无输出）
```

### CLI smoke

```text
$ go run ./cmd/whiteboard validate --root .
exit 0
validation passed

$ go run ./cmd/whiteboard coverage --root . --format json
exit 0
{
  "baseline_total": 75,
  "complete_total": 0,
  "missing_case_ids": [75 个冻结 canonical case ID]
}
```

### Modules

普通 `go mod tidy` 被当前环境的 Go proxy 连接拒绝阻断，失败仅发生在 `go.yaml.in/yaml/v3` 的上游测试依赖，不是本任务生产依赖解析失败：

```text
$ go mod tidy
exit 1
gopkg.in/check.v1@v0.0.0-20161208181325-20d25e280405: Get "https://proxy.golang.org/...zip": proxyconnect tcp: ... connect: connection refused
```

使用 Go 官方容错选项完成可达部分，并确认模块文件未被改变：

```text
$ go mod tidy -e
exit 0

$ git diff --exit-code -- go.mod go.sum
exit 0（无差异）

$ go mod verify
all modules verified
```

`go.mod` 仍恰好保留两项 direct dependency：Goldmark 1.8.4 与 YAML v3 3.0.4。

## 规格裁决

- Goldmark 默认 heading ID 不适合作为跨版本仓库契约，因此 fragment 使用显式、测试冻结的 Unicode slug 实现。
- HTML block 整块不计 prose/unfinished/claim；inline raw HTML 为零宽忽略，其相邻可见文本仍参与检查。
- `MEASURED` 的 W0 条件止于 required run 对 binding 的静态闭包；不在 Task 4 擅自要求尚不存在的 evidence snapshot。
- link destination 按 Goldmark unescape 后交给 URL parser 解码一次；不进行递归解码。
- 诊断消息不泄漏绝对临时路径或平台错误文本，排序键保持 `(code, path, entity_id, message)`。

## 独立复审

独立 reviewer 的真实结论为不批准：Critical 0、Important 5、Minor 1。五项 Important 分别是 canonical README fail-open、atomic coverage 错误泄漏随机/绝对路径、未闭合 claim scanner fail-open、CLI writer failure 未统一映射 exit 2，以及 text diagnostic 的 CR/LF/ANSI 注入。Minor 是 reserved-token 非 word 边界与 malformed 重叠恢复不完整。Reviewer 未修改或提交文件；所有 finding 均进入下述追加修复循环。

## 风险与遗留

唯一非代码限制是当前环境无法连接 Go proxy 下载一个上游测试依赖；现有 module cache 足以让本仓库 full/race/vet/CLI 全部通过，模块文件保持不变。除此之外无已知阻塞。

## 独立复审追加修复循环

首个 Task 4 commit `9be1251cf0162f1e96d3e339ccc38add4a4ceb0c` 之后，控制端独立复审给出 C0/I5/M1。修复保持 Task 4 范围，未修改 catalog/validator、规范、真实内容、harness 或 evidence；不 amend 首个 commit。

### Fix 1：canonical complete README fail closed

根因是 case/principle canonical README 在 lexical、case、symlink 和 file type 检查前直接调用 `os.ReadFile`。先增加共用 reader 的回归测试。

RED：

```text
$ go test ./internal/content -run '^TestReadCanonicalMarkdownRejectsUnsafeAuthoredPaths$' -count=1
exit 1
internal/content/links_test.go:211:24: undefined: readCanonicalMarkdown
internal/content/links_test.go:226:24: undefined: readCanonicalMarkdown
internal/content/links_test.go:234:24: undefined: readCanonicalMarkdown
internal/content/links_test.go:245:24: undefined: readCanonicalMarkdown
```

实现后进一步加入“仓库内中间目录 symlink”用例，证明只检查最终路径仍会读取 symlink 目标。

```text
$ go test ./internal/content -run '^TestReadCanonicalMarkdownRejectsUnsafeAuthoredPaths/internal_directory_symlink$' -count=1
exit 1
data=<outside canonical Markdown> diagnostics=[]; want intermediate symlink failure
```

GREEN：

```text
$ go test ./internal/content -run '^TestReadCanonicalMarkdownRejectsUnsafeAuthoredPaths$' -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 0.972s
```

case/principle 现共用一个 canonical reader。它先拒绝非单路径组件和 lexical escape，再从 root 下逐组件 `Lstat`，在进入任何中间 symlink 前失败；之后校验 exact case、最终 regular file 和 real-root containment，最后才读取。回归覆盖 `../`、外部 symlink、内部目录 symlink、大小写不精确和 special file。

### Fix 2：claim scanner fail-open 与 reserved-token 边界

根因是 scanner 在首个无 `]` 的普通 `[` 处直接 `break`，并在 malformed 外层 marker 后跳到 `]` 之后，从而吞掉重叠的合法 marker。reserved-class 候选边界也只认 ASCII colon/space。

RED：

```text
$ go test ./internal/content -run 'TestClaimCandidateDetectionIsReservedAndCaseInsensitive|TestOrdinaryUnclosedBracketDoesNotHideLaterMalformedClaimMarker|TestMalformedReservedMarkerDoesNotHideNestedValidClaimMarker' -count=1
exit 1
scanClaimMarkers("[MEASURED-fake-claim]") malformed=[]; want one malformed reserved marker
scanClaimMarkers("[note [DEDUCED:claim-one") malformed=[]; want nested unclosed reserved marker
scanClaimMarkers("[DEDUCED:bad [DEDUCED:claim-one]") markers=[]; want nested valid DEDUCED claim
```

GREEN：

```text
$ go test ./internal/content -run 'TestClaimCandidateDetectionIsReservedAndCaseInsensitive|TestOrdinaryUnclosedBracketDoesNotHideLaterMalformedClaimMarker|TestMalformedReservedMarkerDoesNotHideNestedValidClaimMarker' -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 0.457s
```

scanner 在 ordinary/malformed 起点后一个 byte 恢复扫描，只在合法 marker 后跳过完整 marker。reserved token 后若继续 Unicode identifier word 才视为普通词，因此 `[DEDUCEDLY:note]` / `[ASSUMEDNESS]` 仍普通，而 `[MEASURED-fake-claim]` 与全角冒号形式均稳定报 malformed。排序阶段继续对完全相同诊断去重。

### Fix 3：atomic coverage error 稳定化

根因是 `CreateTemp` / `Chmod` / `Write` / `Close` / `Rename` 的平台错误被原样拼入 stderr，包含绝对路径和随机 `.whiteboard-coverage-*` 名称。

RED：

```text
$ go test ./internal/cli -run '^TestCoverageCommandOutputRequiresExistingParentAndCleansTempOnFailure$' -count=1
exit 1
first stderr contains .whiteboard-coverage-3069797116
second stderr contains .whiteboard-coverage-1603279915
want byte-identical stable stage error
```

GREEN 包含在 Fix 4 的 focused 命令中。atomic writer 现在只返回固定阶段：create、chmod、write、close 或 replace；CLI 不再打印 output 绝对路径或底层平台错误。失败后仍关闭并移除 temp，rename 失败不会损坏旧目标。

### Fix 4：所有 CLI writer failure 统一退出 2

根因是 `fmt`/`flag` 写入、stdout、check 消息和若干诊断路径忽略 `(n < len, nil)` 或 writer error。先用同一组 short/error writer 覆盖 help、flag、validate/coverage stdout、diagnostics 以及 check missing/mismatch。

RED：

```text
$ go test ./internal/cli -run 'TestRunMapsEveryAttemptedWriterFailureToExitTwo|TestTextDiagnosticsEscapeRepositoryControlledFields|TestCoverageCommandOutputRequiresExistingParentAndCleansTempOnFailure' -count=1
exit 1
short/help exit=0; want 2
short/validate_stdout exit=0; want 2
short/validate_diagnostics exit=3; want 2
short/coverage_stdout exit=0; want 2
short/coverage_diagnostics exit=3; want 2
short/check_missing exit=3; want 2
short/check_mismatch exit=3; want 2
error/help exit=0; want 2
error/check_missing exit=3; want 2
error/check_mismatch exit=3; want 2
```

顶层 `Run` 现在把 stdout/stderr 包装为 tracking writer；任何 attempted write 的 short write 或 error 最终覆盖为 exit 2。完整 byte slice 输出统一经 `writeFull`，flag/help 内部即使忽略 error，顶层 tracker 仍保留失败。

### Fix 5：text diagnostic 控制字符转义

根因是 text renderer 直接插入仓库可控的 path/entity/message，使 CR、LF 与 ESC 可以制造物理行或终端控制序列。

RED：

```text
text diagnostic contains injectable controls:
"error [missing_link_target] path=bad\n\x1b[31merror [forged].md entity=entity\r..."
```

text renderer 现在按单条诊断构造一条物理行，并用 `strconv.Quote` 转义三个仓库字段；正常方括号保持可读，避免破坏 `missing_ids=[...]` 等既有文本。JSON 仍通过结构化字段保留原始值。真实含 LF/ESC 文件名回归确认 stderr 只有一条物理诊断行，且没有原始 CR/LF/ESC。

Fix 3–5 focused GREEN：

```text
$ go test ./internal/cli -run 'TestRunMapsEveryAttemptedWriterFailureToExitTwo|TestTextDiagnosticsEscapeRepositoryControlledFields|TestCoverageCommandOutputRequiresExistingParentAndCleansTempOnFailure' -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 0.277s
```

### 修复后 fresh 验证

```text
$ go test ./internal/content -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 0.366s

$ go test ./internal/cli -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/cli 0.559s

$ go test ./... -count=1
exit 0
catalog 0.458s; cli 0.754s; content 1.034s; validator 1.414s

$ go test -race ./... -count=1
exit 0
catalog 2.788s; cli 3.139s; content 3.780s; validator 4.005s

$ go vet ./...
exit 0（无输出）

$ gofmt -d internal/content internal/cli cmd/whiteboard
exit 0（无输出）

$ git diff --check
exit 0（无输出）

$ go run ./cmd/whiteboard validate --root .
exit 0
validation passed

$ go run ./cmd/whiteboard coverage --root . --format json
exit 0
baseline_total=75; complete_total=0; missing_case_ids=75
```

修复代码已完成 fresh 验证，当前状态为待独立 reviewer fresh rereview；本报告不提前声称 Approved。

## Fresh rereview：resolved repository root

第二轮 fresh rereview 结论为 Critical 0、Important 1、Minor 0，未批准。唯一 Important 是 `ValidateRepository` 虽已计算 `rootReal`，但仍把未解析的 `rootAbsolute` 交给 `WalkDir`。当 CLI 的 `--root` 本身是目录 symlink 时，`WalkDir` 把入口视为 symlink 而不下钻，仓库 Markdown link checks 因此 fail-open。

### RED

先创建真实 root 的 `README.md`，其中包含缺失相对目标；同一仓库分别通过真实路径和目录 symlink 调用。目标契约是两次都返回完全相同的 `missing_link_target`。

```text
$ go test ./internal/content -run '^TestValidateRepositoryResolvesRootSymlinkBeforeWalking$' -count=1
exit 1
symlink-root diagnostics=[]
want direct-root diagnostics=[missing_link_target path=README.md]
```

### GREEN

`ValidateRepository` 现在只用 `rootAbsolute` 解析信任锚；`EvalSymlinks` 成功后，将清理后的 `rootReal` 统一用于：

- case/principle canonical README 安全读取；
- Markdown discover / load / relative path；
- link target containment、cache key 与 fragment 解析。

root 自身作为用户选择的信任锚允许是 symlink，但 resolved root 之下的 canonical 中间 symlink 拒绝规则保持不变。

```text
$ go test ./internal/content -run '^TestValidateRepositoryResolvesRootSymlinkBeforeWalking$' -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 0.984s
```

### 修复后 fresh 验证

```text
$ go test ./internal/content -count=1
exit 0
ok github.com/PinoHouse/works-on-my-whiteboard/internal/content 2.068s

$ go test ./... -count=1
exit 0
catalog 0.965s; cli 0.619s; content 1.724s; validator 1.411s

$ go test -race ./... -count=1
exit 0
catalog 3.276s; cli 3.568s; content 4.487s; validator 3.736s

$ go vet ./...
exit 0（无输出）

$ gofmt -d internal/content internal/cli cmd/whiteboard
exit 0（无输出）

$ git diff --check
exit 0（无输出）

$ go run ./cmd/whiteboard validate --root .
exit 0
validation passed

$ go run ./cmd/whiteboard coverage --root . --format json
exit 0
baseline_total=75; complete_total=0; missing_case_ids=75
```

该 I1 已完成 RED/GREEN 与全量验证，当前状态为待下一次 fresh rereview；本报告不提前声称 Approved。
