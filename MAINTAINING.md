# 维护说明（Hei-XiaoHu fork）

四个仓库都只保留 `main` 分支，上游更新一律用 **merge** 合入 `main`（不 rebase、不强推），
这样 `go.mod` 里引用过的旧提交永远可下载。

| 仓库 | 上游 (`upstream`) | 我们在上游之上的补丁 |
|---|---|---|
| [Hei-XiaoHu/Xboard-Node](https://github.com/Hei-XiaoHu/Xboard-Node) | cedar2025/Xboard-Node `dev` | 内核版本升级与适配 |
| [Hei-XiaoHu/Xray-core](https://github.com/Hei-XiaoHu/Xray-core) | XTLS/Xray-core 发布 tag | 按用户限速 (`features/bandwidth`, `app/dispatcher/ratelimit.go`) |
| [Hei-XiaoHu/sing-box](https://github.com/Hei-XiaoHu/sing-box) | SagerNet/sing-box 发布 tag | UpdateUsers/UpdateRules、mieru、DynamicInboundManager、并发安全的换用户、`block_destinations`（防 SSRF） |
| [Hei-XiaoHu/sing-quic](https://github.com/Hei-XiaoHu/sing-quic) | SagerNet/sing-quic `main` | hysteria/hysteria2/tuic 用户表原子替换 |

Xray-core / sing-box 还有一个只读远程 `cedar`，用来查看原作者是否更新了补丁。

本地路径：`~/桌面/proxy/{Xboard-Node,Xray-core,sing-box,sing-quic}`。

注意：`replace` 只在主模块生效。sing-box 的 go.mod 里替换了 sing-quic，
但 Xboard-Node 的 go.mod 里**也必须**有同样的 sing-quic replace，否则会悄悄用回上游版本。
Go 版本跟随 Xray（目前要求 1.27）；本机 Go 较旧时命令前加 `GOTOOLCHAIN=auto`。

## 升级 Xray-core

```bash
cd ~/桌面/proxy/Xray-core
git fetch upstream --tags
git merge vX.Y.Z            # 上游新版本 tag，解决冲突
go build ./... && go test ./app/dispatcher/ ./common/buf/ ./common/mux/ ./features/bandwidth/ ./core/
git push origin main
```

## 升级 sing-box

```bash
cd ~/桌面/proxy/sing-box
git fetch upstream --tags
git merge vX.Y.Z
git push origin vX.Y.Z      # 推上游 tag，让 Go 伪版本号显示为 vX.Y.(Z+1)-0.xxx
TAGS="with_quic with_utls with_wireguard with_acme with_clash_api"
go build -tags "$TAGS" ./cmd/sing-box ./protocol/... ./route/... ./adapter/...
go test -tags "$TAGS" ./adapter/... ./route/... ./protocol/...
git push origin main
```

升级 sing-box 后，看它的 go.mod 用的是哪个 sing-quic 版本，把 sing-quic 合并到那个版本：

```bash
cd ~/桌面/proxy/sing-quic
git fetch upstream
git merge <sing-box go.mod 里 sing-quic 的提交 hash>
go build ./... && git push origin main
# 然后 sing-box 和 Xboard-Node 都更新 sing-quic 的 replace
```

升级 sing-box 时重点关注这些 xboard 改动是否还能编译/语义正确：
`adapter/inbound.go`、`adapter/inbound/manager.go`、`adapter/lifecycle.go`(Scope.Remove)、
`route/router.go`(UpdateRules)、`protocol/*/user.go`、`protocol/mieru/`、
`common/dialer/destination_guard.go` 与 `common/dialer/default.go` 里的 guard 调用。

## 更新 Xboard-Node

```bash
cd ~/桌面/proxy/Xboard-Node
git fetch upstream
git merge upstream/dev      # 合入原作者的更新；go.mod 的 replace 冲突保留我们的

# 指向新的内核提交（填 Xray-core / sing-box main 的提交 hash）
go mod edit -replace=github.com/xtls/xray-core=github.com/Hei-XiaoHu/Xray-core@<commit>
go mod edit -replace=github.com/sagernet/sing-box=github.com/Hei-XiaoHu/sing-box@<commit>
go mod edit -replace=github.com/sagernet/sing-quic=github.com/Hei-XiaoHu/sing-quic@<commit>
go mod tidy

make test
make test-runtime
make build-linux build-linux-arm64
git push origin main
```

`make test-runtime` 会为两个内核真正启动节点：检查用户无法通过节点访问本机
（IP、`localhost`、`0.0.0.0`、IPv4 映射地址、UDP 夹带都要被拦），并为每种协议真正启动 sing-box，走一遍 Reload / Stop，
并在不断有连接握手的同时反复 UpdateUsers（开 `-race`）；对 hysteria/hysteria2/tuic
还会用真实客户端在换用户期间持续连接，并验证删掉的用户连不上、新加的用户能连上。
内核升级后务必跑一次。

## 注意事项

- **防 SSRF（防止用户借节点访问节点所在机器/内网）：** 拦截列表在
  `internal/kernel/ssrf.go`，两个内核共用。路由规则只能匹配 IP，真正兜底的是直连出站
  （sing-box 的 `block_destinations`、Xray freedom 的 `finalRules`），它们在 DNS 解析之后、
  并对每个 UDP 包检查。面板里的路由“放行”规则放行不了这些地址；确实需要访问内网时，
  在 `kernel.custom_outbound` 里自定义一个 tag 为 `direct` 的出站来覆盖默认直连（风险自负）。

- 仓库里有一个名为 `dev` 的 tag，推送时请写全分支名：`git push origin refs/heads/main`。
