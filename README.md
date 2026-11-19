# kos-diagnostics

KOS 系统应用项目：诊断页与诊断包导出。页面只读呈现降级原因、阻塞对象与证据链，唯一动作是诊断包导出。

- 设计：`tsic/kos-design` 仓的 `kos-diagnostics/`（范围、契约、验收、决策记录）
- 交付形态：独立扩展包，装成系统组件；默认 UI-only，不带自有后端（ADR-033）

## 模块

| 模块 | 职责 | 契约 |
|---|---|---|
| `declaration.go` | 页面、展示项与导出动作的声明；贡献随组件就绪激活、停用撤销 | `DIAG-C-1`、`KOSD-C-18`、`KOSD-C-28` |
| `readings.go` | 条目读取的分派与可见性 | `KOSD-C-53`、`KOSD-C-59` |
| `health.go` | 降级原因与建议动作（计算源：健康与条件的聚合，应用另取证据链结论） | `DIAG-C-1`、`KOSD-C-53` |
| `blocked.go` | 卸载卡住与阻塞对象（对象源：应用阶段、阻塞清单与卷的阻塞条件） | `DIAG-C-1`、`KOSD-C-33`、`KOSD-C-38` |
| `evidence.go` | 各段证据与事实/推断区分 | `DIAG-C-1`、`KOSD-C-26` |
| `export.go` | 诊断包导出入口与导出目标 | `DIAG-C-1`、`KOSD-C-12`、`KOSD-C-26` |
| `manifest.go` | 同一份声明生成的 kos-sdk 清单：`KOSD-C-18` 的封闭字段集合 | `SDK-C-3` |
| `package.go` | 经 kos-sdk 打包：清单、UI 资产与包摘要 | `SDK-C-4`、`KOSD-C-17` |
| `cmd/kos-diagnostics-pack/` | 打包入口：写出扩展包与摘要 | `KOSD-C-17`、`SDK-C-4` |

模块不持有状态：证据与诊断包读时生成，不落库。

## 页面

| 展示项 | 来源类型 | 内容 |
|---|---|---|
| 降级原因与建议动作 | 计算源 | 聚合规则与判定过的对象 |
| 卸载卡住与阻塞对象 | 对象源 | 卡住的应用与阻塞清单中的对象 |
| 各段证据 | 对象源 | 引用链逐段证据，扩展段按声明段位插入 |
| 诊断包导出 | 计算源 | 可导出的应用；动作 `POST /api/v1/diagnostics/bundles` |

页面只读：非导出项各自声明只读原因，页面唯一动作是导出；导出读时生成，不写对象。导出入口随组件启停出现与消失。

## 边界

- 证据链组装与诊断包组装留在 kosd（`pkg/diagnostics`，`KOSD-C-26`）；本仓只经控制面读取，不复制其判定。
- 不保存日志，诊断包不含日志内容（`KOSD-C-64`）；不导出凭据或敏感参数（`KOSD-C-14`）。
- 不带自有后端、控制器、对象类或新 API（ADR-033）。

## 扩展包

扩展包是系统组件的安装输入：`SystemComponent.spec.package` 引用工件并按摘要校验，安装路径不认产品名（`KOSD-C-17`）。第一方与第三方共用 kos-sdk 的打包路径，同一输入产出同一结构（`SDK-C-4`）。

| 条目 | 内容 |
|---|---|
| `manifest.json` | `Declaration()` 渲染的清单，字段集合等于 `KOSD-C-18` 的封闭集合；不新增字段，缺项不补默认值 |
| `assets/<path>` | 清单声明的 UI 资产；本产品不声明 UI 资产，因此裸包没有这一项 |
| `digest` | 覆盖清单与全部 UI 资产的 `sha256:<hex>` 摘要 |

声明不带 UI 资产，所以不提供资产时包只有 `manifest.json` 与 `digest` 两个条目；打包器不凭空造资产，也不为凑结构补写未声明的贡献。

清单要点：

- 标识 `kos-diagnostics`、版本 `1.0.0`、目标框架范围 `>=1.1 <2.0`。
- 贡献只有诊断页（含导出动作）；没有对象类、控制器、能力、设置项。
- `apiGroup` 缺省：本产品不贡献对象类，不虚构 API 组（`KOSD-C-19`）。
- 依赖只到框架范围，不依赖其他扩展；权限只声明页面读取范围与所需控制面角色，页面只读，无写入范围与对象写权限。
- 组件配置结构与状态接口为空，卸载保留语义为空：页面没有自有状态，卸载不留下资源（`KOSD-C-18`、`KOSD-C-29`）。

```sh
make pack   # 写出 dist/kos-diagnostics.tar 与 dist/kos-diagnostics.tar.digest
```

`make pack` 读 `assets/`（缺失即无 UI 资产），校验清单并产出带 `sha256:<hex>` 摘要的包；同一清单与资产总是产出同一结构与同一摘要。

## 装载与卸载

装载与卸载走系统组件的唯一通道，不新增专用 API 或字段（`KOSD-C-17`、`KOSD-C-29`）：

1. 把包放到安装路径可读取的工件地址，创建集群作用域的 `SystemComponent`，`spec.package.reference` 指向该地址、`spec.package.digest` 写 `make pack` 输出的摘要；摘要不匹配时安装被拒绝。
2. 安装路径按声明装载页面贡献。控制面宿主在页面壳装配处按产品标识 `kos-diagnostics` 注册本产品的读取器（`pages.Readings`）到 `pages.Router`；读取器只解析服务端条目，条目引用的产品没有接入时按 `CapabilityMissing` 拒绝，不给编造值。
3. 页面是否存在只由激活声明经 `PageFor` 决定：装载后 `/api/v1/pages` 列出诊断页、`/api/v1/pages/diagnostics` 可读且条目有值；卸载（删除 `SystemComponent` 或停用组件）后该页与导出动作消失，读取器即使仍在路由表中也不出页面。

宿主注入读取器时使用 `Product{Dynamic, Environment, Now}`（或 `NewReader`）：`Dynamic` 是控制面集群客户端，`Environment` 是诊断链报告的扩展状态，`Now` 是读取时钟。产品不自带后端、不持有凭据，读取按调用者会话裁剪可见对象（`KOSD-C-59`）。

## 构建与测试

```sh
make lint        # gofmt 检查 + go vet
make test        # 单元与验收用例
make acceptance  # 只跑 tests/acceptance/，-v
make pack        # 写出扩展包与摘要
```

验收用例：`tests/acceptance/DIAG-TC-1/`、`DIAG-TC-12/`、`DIAG-TC-13/`、`DIAG-TC-89/`（均 P1），以及 `tests/acceptance/KOSD-TC-155/`（P0：包结构、清单字段集合、条目与声明一一对应、摘要覆盖）。
用例读的是内存 API server：证据链与诊断包由 kosd 的真实实现组装，世界夹具见 `tests/acceptance/fixture/`。
