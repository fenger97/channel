// Server：UDP 代理，固定模式支持 -queue=channel|ringbuffer|disruptor。
//
// 已知可复现现象：若将 disruptor 相关代码注释掉后仅保留 channel+ringbuffer 编译，
// 同一环境下 -queue=channel 的吞吐会明显低于“三种模式均参与编译”时的 channel。
// 推测与二进制中代码/数据布局或编译器对 channel 热路径的优化差异有关。
// 若需稳定的 channel 基线，请保持本文件中 channel/ringbuffer/disruptor 均参与编译，仅通过 -queue 选择运行哪种。
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"sync/atomic"
	"time"
)

// 协议说明: 17 字节 Header
// Byte 0: Type (1-Handshake, 2-Data, 3-Reply)
// Bytes 1-4: Session ID (uint32, BigEndian)
// Bytes 5-8: Sequence Number (uint32, BigEndian)
// Bytes 9-16: Timestamp (int64, BigEndian)

const (
	TypeHandshake uint8 = 1
	TypeData      uint8 = 2
	TypeReply     uint8 = 3
)

const (
	QueueChannel = "channel"
)

// 单条数据最大长度，ringbuffer/disruptor 共用
const maxDataLen = 2048

type DataPacket struct {
	RemoteAddr *net.UDPAddr
	Data       []byte
}

type Session struct {
	ID          uint32
	Conn        *net.UDPConn // 在动态端口模式下使用
	BackendConn *net.UDPConn // 到目标服务器的连接
	TargetAddr  *net.UDPAddr
	ClientAddr  atomic.Value // 存储 *net.UDPAddr，用于回包
	Done        chan struct{}
	LastActive  int64 // UnixNano
	DataChan    chan *DataPacket
}

func (s *Session) startTask(conn *net.UDPConn, initialClientAddr *net.UDPAddr) {
	fmt.Printf("Task for Session %d started, proxying to %v\n", s.ID, s.TargetAddr)
	defer fmt.Printf("Task for Session %d stopped\n", s.ID)

	s.ClientAddr.Store(initialClientAddr)

	// 后端接收协程
	go func() {
		buf := make([]byte, 2048)
		for {
			select {
			case <-s.Done:
				return
			default:
				s.BackendConn.SetReadDeadline(time.Now().Add(1 * time.Second))
				n, _, err := s.BackendConn.ReadFromUDP(buf)
				if err != nil {
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					return
				}

				// 将后端回复封装协议头部并发送回客户端
				resp := make([]byte, 5+n)
				resp[0] = TypeReply
				binary.BigEndian.PutUint32(resp[1:5], s.ID)
				copy(resp[5:], buf[:n])

				cAddr := s.ClientAddr.Load().(*net.UDPAddr)
				if cAddr != nil {
					conn.WriteToUDP(resp, cAddr)
				}
			}
		}
	}()

	if s.DataChan != nil {
		for {
			select {
			case packet := <-s.DataChan:
				s.ClientAddr.Store(packet.RemoteAddr)
				_, err := s.BackendConn.Write(packet.Data)
				if err != nil {
					fmt.Printf("Session %d forward to backend error: %v\n", s.ID, err)
				}
			case <-s.Done:
				if s.BackendConn != nil {
					s.BackendConn.Close()
				}
				return
			}
		}
	}

	// 动态模式：无队列
	<-s.Done
	if s.BackendConn != nil {
		s.BackendConn.Close()
	}
}

type PortHandler struct {
	conn        *net.UDPConn
	sessions    map[uint32]*Session
	sessionLock sync.RWMutex
}

var (
	portHandlers []*PortHandler
	handlerLock  sync.Mutex
	lastID       uint32
	mode         string // "fixed" or "dynamic"
	queueMode    string // "channel"，仅 fixed 模式有效
	listenIP     string
)

func main() {
	var portRangeStart int
	var fixedPorts int
	flag.StringVar(&mode, "mode", "fixed", "Test mode: fixed or dynamic")
	flag.StringVar(&queueMode, "queue", "channel", "In fixed mode: channel, ringbuffer, or disruptor")
	flag.StringVar(&listenIP, "listen-ip", "0.0.0.0", "IP address to listen on")
	flag.IntVar(&portRangeStart, "start-port", 10000, "Starting port for listeners")
	flag.IntVar(&fixedPorts, "fixed-ports", 10, "Number of fixed ports to listen on in fixed mode")
	flag.Parse()

	if mode == "fixed" && queueMode != QueueChannel {
		fmt.Printf("invalid -queue=%s, use channel\n", queueMode)
		return
	}
	fmt.Printf("Starting UDP Echo Server in %s mode", mode)
	if mode == "fixed" {
		fmt.Printf(" (queue=%s)", queueMode)
	}
	fmt.Println("...")

	// 统一不论模式如何，都监听 fixedPorts 数量的入口
	for i := 0; i < fixedPorts; i++ {
		port := portRangeStart + i
		go listenFixed(port)
	}

	// 启动 pprof 监听
	go func() {
		fmt.Println("Pprof server starting on :6060")
		if err := http.ListenAndServe("0.0.0.0:6060", nil); err != nil {
			fmt.Printf("Pprof server failed: %v\n", err)
		}
	}()

	go cleanIdleSessions()

	select {}
}

func cleanIdleSessions() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	const timeout = 5 * time.Minute

	for range ticker.C {
		now := time.Now().UnixNano()
		handlerLock.Lock()
		activeHandlers := make([]*PortHandler, len(portHandlers))
		copy(activeHandlers, portHandlers)
		handlerLock.Unlock()

		for _, h := range activeHandlers {
			h.sessionLock.Lock()
			for id, session := range h.sessions {
				if now-atomic.LoadInt64(&session.LastActive) > int64(timeout) {
					fmt.Printf("Session %d on %v timed out, cleaning up...\n", id, h.conn.LocalAddr())
					close(session.Done)
					delete(h.sessions, id)
				}
			}
			h.sessionLock.Unlock()
		}
	}
}

func listenFixed(port int) {
	addr, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", listenIP, port))
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Printf("Error listening on port %d: %v\n", port, err)
		return
	}
	defer conn.Close()

	h := &PortHandler{
		conn:     conn,
		sessions: make(map[uint32]*Session),
	}

	handlerLock.Lock()
	portHandlers = append(portHandlers, h)
	handlerLock.Unlock()

	fmt.Printf("Listening on fixed port %d\n", port)

	for {
		buf := make([]byte, 2048)
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			continue
		}
		if n < 5 {
			continue
		}

		packetType := buf[0]
		sessionID := binary.BigEndian.Uint32(buf[1:5])

		switch packetType {
		case TypeHandshake:
			handshakePayload := make([]byte, n-5)
			copy(handshakePayload, buf[5:n])
			go h.handleHandshake(remoteAddr, handshakePayload)
		case TypeData:
			h.handleData(remoteAddr, sessionID, buf[5:n])
		}
	}
}

func (h *PortHandler) handleHandshake(remoteAddr *net.UDPAddr, payload []byte) {
	newID := atomic.AddUint32(&lastID, 1)

	// 解析目标地址
	if len(payload) < 1 {
		fmt.Printf("Invalid handshake payload length from %v\n", remoteAddr)
		return
	}
	addrLen := int(payload[0])
	if len(payload) < 1+addrLen {
		fmt.Printf("Handshake payload too short for address from %v\n", remoteAddr)
		return
	}
	targetAddrStr := string(payload[1 : 1+addrLen])
	targetUDPAddr, err := net.ResolveUDPAddr("udp", targetAddrStr)
	if err != nil {
		fmt.Printf("Failed to resolve target address %s: %v\n", targetAddrStr, err)
		return
	}

	// 建立到后端的连接
	backendConn, err := net.DialUDP("udp", nil, targetUDPAddr)
	if err != nil {
		fmt.Printf("Failed to dial target %v: %v\n", targetUDPAddr, err)
		return
	}

	session := &Session{
		ID:          newID,
		LastActive:  time.Now().UnixNano(),
		TargetAddr:  targetUDPAddr,
		BackendConn: backendConn,
	}
	session.ClientAddr.Store(remoteAddr)

	if mode == "dynamic" {
		// 动态模式：新开一个端口并告知客户端
		dynamicConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(listenIP), Port: 0})
		if err == nil {
			session.Conn = dynamicConn
			_, localPort, _ := net.SplitHostPort(dynamicConn.LocalAddr().String())
			fmt.Printf("Session %d created on dynamic port %s\n", newID, localPort)

			session.Done = make(chan struct{})
			go session.startTask(dynamicConn, remoteAddr)

			go serveDynamic(session)

			// 返回给客户端：TypeReply + SessionID + LocalPort
			resp := make([]byte, 7)
			resp[0] = TypeReply
			binary.BigEndian.PutUint32(resp[1:5], newID)

			// 提取端口并写入响应
			_, portStr, _ := net.SplitHostPort(dynamicConn.LocalAddr().String())
			var portNum uint16
			fmt.Sscanf(portStr, "%d", &portNum)
			binary.BigEndian.PutUint16(resp[5:7], portNum)

			h.conn.WriteToUDP(resp, remoteAddr)
		}
	} else {
		// 固定模式：按 -queue 创建 channel / ringbuffer / disruptor
		session.Done = make(chan struct{})
		switch queueMode {
		case QueueChannel:
			session.DataChan = make(chan *DataPacket, 100)
		}
		go session.startTask(h.conn, remoteAddr)

		resp := make([]byte, 5)
		resp[0] = TypeReply
		binary.BigEndian.PutUint32(resp[1:5], newID)
		h.conn.WriteToUDP(resp, remoteAddr)
	}

	h.sessionLock.Lock()
	h.sessions[newID] = session
	h.sessionLock.Unlock()
}

func (h *PortHandler) handleData(remoteAddr *net.UDPAddr, id uint32, data []byte) {
	h.sessionLock.RLock()
	session, ok := h.sessions[id]
	h.sessionLock.RUnlock()

	if !ok {
		return
	}
	atomic.StoreInt64(&session.LastActive, time.Now().UnixNano())
	session.ClientAddr.Store(remoteAddr)

	switch queueMode {
	case QueueChannel:
		packet := &DataPacket{RemoteAddr: remoteAddr, Data: append([]byte(nil), data...)}
		select {
		case session.DataChan <- packet:
		default:
			fmt.Printf("Session %d data channel full, dropping packet\n", id)
		}
	}
}

func serveDynamic(session *Session) {
	conn := session.Conn
	defer conn.Close()
	defer close(session.Done)

	buf := make([]byte, 2048)
	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			break
		}
		if n < 5 {
			continue
		}
		// 在动态端口上收到的包，必然是该 Session 专属
		packetType := buf[0]
		if packetType == TypeData {
			// 直接转发到后端，不再通过 Channel
			data := buf[5:n]
			val := session.ClientAddr.Load()
			if val == nil {
				session.ClientAddr.Store(remoteAddr)
			} else {
				sAddr := val.(*net.UDPAddr)
				if sAddr.String() != remoteAddr.String() {
					session.ClientAddr.Store(remoteAddr)
				}
			}
			session.BackendConn.Write(data)
			atomic.StoreInt64(&session.LastActive, time.Now().UnixNano())
		}
	}
}
