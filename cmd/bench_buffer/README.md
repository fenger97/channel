# bench_buffer

对比 **channel**、**[smallnest/ringbuffer](https://github.com/smallnest/ringbuffer)**（及可选 **[smarty/go-disruptor](https://github.com/smarty/go-disruptor)**）在单生产者单消费者场景下传递 `[]byte` 的吞吐，用于评估用环形缓冲区代替 channel 的收益。

## 依赖

- **[smallnest/ringbuffer](https://github.com/smallnest/ringbuffer)**（默认）：线程安全环形缓冲，实现 `io.ReaderWriter`
- **[go-disruptor](https://github.com/smarty/go-disruptor)**（需 Go 1.26+）：LMAX Disruptor。三项对比在同一份 `main.go` 中，编译即得 channel / ringbuffer / disruptor。

首次编译前在项目根目录执行：

```bash
go mod tidy
```

## 编译与运行

```bash
# 在项目根目录
./build.sh bench
# 或
go build -o bench_buffer ./cmd/bench_buffer

./bench_buffer
```

## 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-n` | 1000000 | 传递的消息条数 |
| `-size` | 128 | 每条消息字节数（与 client payload 一致） |
| `-channel-buf` | 100 | channel 的缓冲大小（仅影响 channel 基准） |

示例：

```bash
./bench_buffer -n 5000000 -size 256 -channel-buf 200
```

## 输出说明

对 channel、ringbuffer、disruptor 各跑 5 轮，输出平均耗时、单条 ns/op 和 msg/s。
