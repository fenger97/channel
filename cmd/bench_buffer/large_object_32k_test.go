// 对比「小对象」与「大对象」Session 下 channel 的吞吐：结构体 <32KB 走 size class，
// >32KB 走 large object，减少多 Session 间 false sharing。见项目 README「性能说明」。
package main

import (
	"sync"
	"testing"
)

// Go 的 large object 阈值（runtime 中 _MaxSmallSize）
const sizeClassThreshold = 32 * 1024

const (
	channelBenchSessions = 50   // 并发 session 数
	channelBenchMsgs     = 2000 // 每个 session 传递的消息数
	channelBenchBuf      = 100  // channel 缓冲
	channelBenchMsgSize  = 128  // 每条消息字节数
)

// SmallSessionWithChan 体积 <32KB，多实例可能挤在同一 span，易产生 false sharing
type SmallSessionWithChan struct {
	Ch      chan []byte
	Padding [1024]byte // 使整体 <32KB（chan 约 8 字节）
}

// LargeSessionWithChan 体积 >32KB，按 large object 分配，各占独立 span
type LargeSessionWithChan struct {
	Ch      chan []byte
	Padding [sizeClassThreshold + 1 - 8]byte // 使整体 >32KB
}

type sessionWithChannel interface {
	ch() chan []byte
}

func (s *SmallSessionWithChan) ch() chan []byte { return s.Ch }
func (s *LargeSessionWithChan) ch() chan []byte { return s.Ch }

// runChannelWorkload 对 sessions 中每个 session 跑一次 producer-consumer，共 len(sessions)*msgsPer 条消息
func runChannelWorkload(sessions []sessionWithChannel, msgsPer int, msg []byte) {
	var wg sync.WaitGroup
	for i := range sessions {
		ch := sessions[i].ch()
		wg.Add(2)
		go func(c chan []byte) {
			for j := 0; j < msgsPer; j++ {
				c <- msg
			}
			wg.Done()
		}(ch)
		go func(c chan []byte) {
			for j := 0; j < msgsPer; j++ {
				<-c
			}
			wg.Done()
		}(ch)
	}
	wg.Wait()
}

// BenchmarkChannelSmallObject 多「小对象」Session 同时跑 channel：结构体 <32KB，可能 false sharing。
// 每次迭代共 channelBenchSessions×channelBenchMsgs 条消息；msg/s ≈ 1e14/ns/op。
func BenchmarkChannelSmallObject(b *testing.B) {
	msg := make([]byte, channelBenchMsgSize)
	sessions := make([]*SmallSessionWithChan, channelBenchSessions)
	for i := range sessions {
		sessions[i] = &SmallSessionWithChan{Ch: make(chan []byte, channelBenchBuf)}
	}
	slice := make([]sessionWithChannel, channelBenchSessions)
	for i := range sessions {
		slice[i] = sessions[i]
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runChannelWorkload(slice, channelBenchMsgs, msg)
	}
}

// BenchmarkChannelLargeObject 多「大对象」Session 同时跑 channel：结构体 >32KB，走 large object，减少 false sharing。
// 每次迭代消息数同 BenchmarkChannelSmallObject，可直接对比 ns/op 或算 msg/s。
func BenchmarkChannelLargeObject(b *testing.B) {
	msg := make([]byte, channelBenchMsgSize)
	sessions := make([]*LargeSessionWithChan, channelBenchSessions)
	for i := range sessions {
		sessions[i] = &LargeSessionWithChan{Ch: make(chan []byte, channelBenchBuf)}
	}
	slice := make([]sessionWithChannel, channelBenchSessions)
	for i := range sessions {
		slice[i] = sessions[i]
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runChannelWorkload(slice, channelBenchMsgs, msg)
	}
}
