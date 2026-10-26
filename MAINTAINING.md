# 维护说明（Hei-XiaoHu fork）

三个仓库都只保留 `main` 分支，上游更新一律用 **merge** 合入 `main`（不 rebase、不强推），
这样 `go.mod` 里引用过的旧提交永远可下载。

| 仓库 | 上游 (`upstream`) | 我们在上游之上的补丁 |
|---|---|---|
| [Hei-XiaoHu/Xboard-Node](https://github.com/Hei-XiaoHu/Xboard-Node) | cedar2025/Xboard-Node `dev` | 内核版本升级与适配 |
| [Hei-XiaoHu/Xray-core](https://github.com/Hei-XiaoHu/Xray-core) | XTLS/Xray-core 发布 tag | 按用户限速 (`features/bandwidth`, `app/dispatcher/ratelimit.go`) |
| [Hei-XiaoHu/sing-box](https://github.com/Hei-XiaoHu/sing-box) | SagerNet/sing-box 发布 tag | UpdateUsers/UpdateRules、mieru、DynamicInboundManager |

Xray-core / sing-box 还有一个只读远程 `cedar`，用来查看原作者是否更新了补丁。

本地路径：`~/桌面/proxy/{Xboard-Node,Xray-core,sing-box}`。
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

升级 sing-box 时重点关注这些 xboard 改动是否还能编译/语义正确：
`adapter/inbound.go`、`adapter/inbound/manager.go`、`adapter/lifecycle.go`(Scope.Remove)、
`route/router.go`(UpdateRules)、`protocol/*/user.go`、`protocol/mieru/`。

## 更新 Xboard-Node

```bash
cd ~/桌面/proxy/Xboard-Node
git fetch upstream
git merge upstream/dev      # 合入原作者的更新；go.mod 的 replace 冲突保留我们的

# 指向新的内核提交（填 Xray-core / sing-box main 的提交 hash）
go mod edit -replace=github.com/xtls/xray-core=github.com/Hei-XiaoHu/Xray-core@<commit>
go mod edit -replace=github.com/sagernet/sing-box=github.com/Hei-XiaoHu/sing-box@<commit>
go mod tidy

make test
make test-runtime
make build-linux build-linux-arm64
git push origin main
```

`make test-runtime` 会为每种协议真正启动 sing-box，走一遍 Reload / Stop，
并在不断有连接握手的同时反复 UpdateUsers（开 `-race`），内核升级后务必跑一次。

## 已知问题

- hysteria2 / tuic 的就地换用户仍由 sing-quic 库直接替换内部 map，没有加锁。
  sing-box 层已改为按用户指针识别，不会再串号或越界崩溃，但库内部这次替换
  理论上仍是数据竞争（主要在 arm64 上有极小概率）。彻底修复需要再 fork sing-quic。
- 仓库里有一个名为 `dev` 的 tag，推送时请写全分支名：`git push origin refs/heads/main`。
