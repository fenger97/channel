// // Package main 对比 channel、smallnest/ringbuffer、smarty/go-disruptor 在单生产者单消费者
// // 场景下传递 []byte 的吞吐，用于评估用环形缓冲区代替 channel 的收益。需 Go 1.26+（go-disruptor）。
package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}

// import (
// 	"flag"
// 	"fmt"
// 	"sync"
// 	"sync/atomic"
// 	"time"

// 	"github.com/smallnest/ringbuffer"
// 	"github.com/smarty/go-disruptor"
// )

// const (
// 	defaultMessageSize = 128
// 	defaultCount       = 1_000_000
// 	defaultChannelBuf  = 1024

// 	disruptorBufferSize = 64 * 1024
// 	disruptorBufferMask = disruptorBufferSize - 1
// )

// var (
// 	messageSize = flag.Int("size", defaultMessageSize, "message size in bytes")
// 	count       = flag.Int("n", defaultCount, "number of messages to pass")
// 	channelBuf  = flag.Int("channel-buf", defaultChannelBuf, "channel buffer size (channel benchmark only)")
// )

// func main() {
// 	flag.Parse()
// 	msg := make([]byte, *messageSize)
// 	for i := range msg {
// 		msg[i] = byte(i % 256)
// 	}

// 	fmt.Printf("Benchmark: %d messages, %d bytes each (channel buf=%d)\n\n", *count, *messageSize, *channelBuf)

// 	runBench("channel", func() { benchmarkChannel(msg, *count, *channelBuf) })
// 	runBench("ringbuffer", func() { benchmarkRingbuffer(msg, *count) })
// 	runBench("disruptor", func() { benchmarkDisruptor(msg, *count) })
// }

// func runBench(name string, fn func()) {
// 	const rounds = 5
// 	var total time.Duration
// 	for i := 0; i < rounds; i++ {
// 		elapsed := measure(fn)
// 		total += elapsed
// 	}
// 	avg := total / rounds
// 	perOp := avg.Nanoseconds() / int64(*count)
// 	fmt.Printf("%-12s avg %v  (%d ns/op, %.0f msg/s)\n", name, avg.Round(time.Millisecond), perOp, float64(*count)*1e9/float64(avg.Nanoseconds()))
// }

// func measure(fn func()) time.Duration {
// 	start := time.Now()
// 	fn()
// 	return time.Since(start)
// }

// // benchmarkChannel 模拟 server 中 DataChan 的用法：生产者写，消费者读。
// func benchmarkChannel(msg []byte, n, bufSize int) {
// 	ch := make(chan []byte, bufSize)
// 	var wg sync.WaitGroup
// 	wg.Add(2)

// 	go func() {
// 		defer wg.Done()
// 		for i := 0; i < n; i++ {
// 			ch <- msg
// 		}
// 		close(ch)
// 	}()

// 	go func() {
// 		defer wg.Done()
// 		for range ch {
// 		}
// 	}()

// 	wg.Wait()
// }

// // benchmarkRingbuffer 使用 smallnest/ringbuffer，固定长度消息（无长度前缀），阻塞模式。
// func benchmarkRingbuffer(msg []byte, n int) {
// 	slotSize := len(msg)
// 	rbSize := 65536
// 	if rbSize < slotSize*2 {
// 		rbSize = slotSize * 32
// 	}
// 	rb := ringbuffer.New(rbSize).SetBlocking(true)

// 	var wg sync.WaitGroup
// 	wg.Add(2)

// 	go func() {
// 		defer wg.Done()
// 		for i := 0; i < n; i++ {
// 			for written := 0; written < slotSize; {
// 				nw, _ := rb.Write(msg[written:])
// 				written += nw
// 			}
// 		}
// 	}()

// 	go func() {
// 		defer wg.Done()
// 		readBuf := make([]byte, slotSize)
// 		for i := 0; i < n; i++ {
// 			for got := 0; got < slotSize; {
// 				nr, _ := rb.Read(readBuf[got:])
// 				got += nr
// 			}
// 		}
// 	}()

// 	wg.Wait()
// }

// // benchmarkDisruptor 使用 smarty/go-disruptor，预分配 ring + Reserve/Commit。
// type disruptorSlot struct {
// 	Data [256]byte
// 	Len  int
// }

// var disruptorRing [disruptorBufferSize]disruptorSlot

// func benchmarkDisruptor(msg []byte, n int) {
// 	handler := &disruptorHandler{}
// 	inst, err := disruptor.New(
// 		disruptor.Options.BufferCapacity(disruptorBufferSize),
// 		disruptor.Options.WriterCount(1),
// 		disruptor.Options.NewHandlerGroup(handler),
// 	)
// 	if err != nil {
// 		panic(err)
// 	}
// 	defer func() { _ = inst.Close() }()

// 	go func() {
// 		defer func() { _ = inst.Close() }()
// 		for i := 0; i < n; i++ {
// 			upper := inst.Reserve(1)
// 			slot := &disruptorRing[upper&disruptorBufferMask]
// 			slot.Len = len(msg)
// 			copy(slot.Data[:], msg)
// 			inst.Commit(upper, upper)
// 		}
// 	}()

// 	inst.Listen()
// }

// type disruptorHandler struct{}

// func (h *disruptorHandler) Handle(lower, upper int64) {
// 	for seq := lower; seq <= upper; seq++ {
// 		_ = disruptorRing[seq&disruptorBufferMask]
// 		atomic.AddInt64(&disruptorConsumed, 1)
// 	}
// }

// var disruptorConsumed int64
