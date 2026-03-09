# multi_echo

UDP 代理服务与压测客户端：固定端口/动态端口模式，配合 Locust+Boomer 做负载测试。

## 目录与编译

| 目录/脚本 | 说明 |
|-----------|------|
| `cmd/server` | UDP 代理 server，固定/动态模式 |
| `cmd/client_boomer` | Boomer 压测客户端，上报到 Locust master |
| `cmd/bench_buffer` | channel / ringbuffer / disruptor 吞吐对比（见其下 README） |
| `build.sh` | 编译 server、client、bench 二进制到 `./bin` |
| `start_clients.sh` | 批量启停 client（start/stop/restart） |

```bash
./build.sh          # 全部编译
./build.sh server   # 仅 server
./build.sh client   # 仅 client_boomer
./build.sh bench    # 仅 bench_buffer
```

## Server 配置

Server 可从 **配置文件** 读取配置，未指定时仍使用命令行标志；**命令行标志优先于配置文件**（先解析标志得到默认值，再读配置文件覆盖）。

### 配置文件（config.json）

在项目根目录的 `config.json` 中增加 `server` 段即可，例如：

```json
{
  "server": {
    "mode": "fixed",
    "listen_ip": "0.0.0.0",
    "start_port": 10000,
    "fixed_ports": 10,
    "padding_bytes": 32776,
    "pprof_port": 6060
  }
}
```

| 字段 | 说明 |
|------|------|
| `mode` | 运行模式：`fixed` / `dynamic` |
| `listen_ip` | 监听 IP |
| `start_port` | 起始端口号 |
| `fixed_ports` | 固定模式下的监听端口数量 |
| `padding_bytes` | Session padding 大小（字节），>32768 走 large object，0 表示不分配 |
| `pprof_port` | pprof HTTP 服务端口 |

指定配置文件路径：`./bin/server -config=/path/to/config.json`，默认使用当前目录下的 `config.json`。

### 命令行标志

与配置项对应，用于覆盖或未使用配置文件时的默认值：

- `-config`：配置文件路径（默认 `config.json`）
- `-mode`、`-listen-ip`、`-start-port`、`-fixed-ports`、`-padding-bytes`、`-pprof-port`

示例：`./bin/server -config=config.json` 或 `./bin/server -mode fixed -padding-bytes=0`。

## 性能说明：Session 体积与 channel 性能

### 现象

在固定模式、使用 channel 转发数据时，若把 `Session` 结构体中的「大块字段」（例如原先的 `DisruptorInst` + `DisruptorRing`，或等价的 `Padding`）去掉，使 `Session` 体积变小，则 **channel 热路径的吞吐会明显下降**。

### 原因结论

1. **Go 分配阈值**  
   Go runtime 对「小对象」按 size class 成批分配（≤32KB）；**大于 32KB 的分配**走 **large object** 路径，直接从堆上单独要一块，不与其他小对象挤在同一 span。

2. **False sharing（伪共享）**  
   CPU 按 **cache line**（通常 64 字节）做一致性。当多个 goroutine 写不同 Session 的「热字段」（如 `DataChan`、`Done` 等）时，若这些 Session 被紧凑分配在同一块内存里，不同 Session 的热数据可能落在**同一 cache line**，导致互相 invalidate 缓存，产生伪共享，性能下降。

3. **大 Session 的效果**  
   当 `Session` 体积 **>32KB** 时：
   - 每个 Session 按 large object 分配，在内存中相对孤立；
   - 不同 Session 的热字段几乎不会落在同一 cache line，伪共享大幅减少，channel 性能恢复。

因此：**保持 Session 总大小超过 32KB**（用真实业务字段或同尺寸 `Padding` 均可），即可获得稳定的 channel 性能；与是否使用 disruptor 类型无关，只与「体积是否超过 large object 阈值」有关。

### 代码中的做法

- 在 `cmd/server/main.go` 中，`Session` 含有 `Padding []byte`，大小由 **配置文件** `server.padding_bytes` 或启动参数 **`-padding-bytes`** 指定，默认 `32776`（32*1024+8），保证单个 Session 体积超过 32KB。传 `0` 表示不分配 padding。
- 启动示例：使用 `config.json` 的 `server` 段，或 `./bin/server -mode fixed -padding-bytes=32776` / `-padding-bytes=0` 对比无 padding 时的 channel 性能。
- 若后续改用其他大块字段（如 ringbuffer/disruptor 的缓冲区），只要 Session 总大小仍 >32KB，可去掉 Padding；否则建议保留 Padding 或等价体积字段。

### 参考资料

- [Struct Field Alignment - Go Optimization Guide](https://goperf.dev/01-common-patterns/fields-alignment/)（对齐与 false sharing）
- [100 Go Mistakes #92 - False sharing](https://100go.co/92-false-sharing/)
- Go 源码：`runtime` 中 `_MaxSmallSize = 32768`（32KB）为 small / large 对象分界
