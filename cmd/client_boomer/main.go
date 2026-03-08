package main

import (
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/myzhan/boomer"
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

type Config struct {
	WarpIP        string `json:"warp_ip"`
	WarpPortMin   int    `json:"warp_port_min"`
	WarpPortMax   int    `json:"warp_port_max"`
	ServerIP      string `json:"server_ip"`
	ServerPortMin int    `json:"server_port_min"`
	ServerPortMax int    `json:"server_port_max"`
	LocustAddr    string `json:"locust_addr"`
	IntervalMs    int    `json:"interval"`
	PayloadSize   int    `json:"payload_size"`
	VerboseReport bool   `json:"verbose_report"`
}

var (
	globalConfig      Config
	warpPortCounter   uint32
	serverPortCounter uint32
)

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	return &config, nil
}

func main() {
	var (
		configPath string
		warpIP     string
		serverIP   string
		locust     string
		interval   int
		payload    int
		verbose    bool
	)

	flag.StringVar(&configPath, "config", "config.json", "Path to config file")
	flag.StringVar(&warpIP, "warp-ip", "", "Warp (Proxy) IP address")
	flag.StringVar(&serverIP, "server-ip", "", "Server (Backend) IP address")
	flag.StringVar(&locust, "locust-addr", "", "Locust master address")
	flag.IntVar(&interval, "interval", 0, "Sending interval in milliseconds")
	flag.IntVar(&payload, "payload-size", 0, "UDP payload size in bytes")
	flag.BoolVar(&verbose, "verbose", false, "Enable verbose reporting with addresses")
	flag.Parse()

	// 1. 设置默认值 (符合用户要求)
	globalConfig = Config{
		WarpIP:        "192.168.0.30",
		WarpPortMin:   3000,
		WarpPortMax:   3010,
		ServerIP:      "192.168.0.32",
		ServerPortMin: 5050,
		ServerPortMax: 5060,
		LocustAddr:    "127.0.0.1",
		IntervalMs:    20,
		PayloadSize:   127,
		VerboseReport: false,
	}

	// 2. 从文件加载 (如果存在)
	if fileConfig, err := loadConfig(configPath); err == nil {
		if fileConfig.WarpIP != "" {
			globalConfig.WarpIP = fileConfig.WarpIP
		}
		if fileConfig.WarpPortMin > 0 {
			globalConfig.WarpPortMin = fileConfig.WarpPortMin
		}
		if fileConfig.WarpPortMax > 0 {
			globalConfig.WarpPortMax = fileConfig.WarpPortMax
		}
		if fileConfig.ServerIP != "" {
			globalConfig.ServerIP = fileConfig.ServerIP
		}
		if fileConfig.ServerPortMin > 0 {
			globalConfig.ServerPortMin = fileConfig.ServerPortMin
		}
		if fileConfig.ServerPortMax > 0 {
			globalConfig.ServerPortMax = fileConfig.ServerPortMax
		}
		if fileConfig.LocustAddr != "" {
			globalConfig.LocustAddr = fileConfig.LocustAddr
		}
		if fileConfig.IntervalMs > 0 {
			globalConfig.IntervalMs = fileConfig.IntervalMs
		}
		if fileConfig.PayloadSize > 0 {
			globalConfig.PayloadSize = fileConfig.PayloadSize
		}
	}

	// 3. 命令行参数覆盖
	if warpIP != "" {
		globalConfig.WarpIP = warpIP
	}
	if serverIP != "" {
		globalConfig.ServerIP = serverIP
	}
	if locust != "" {
		globalConfig.LocustAddr = locust
	}
	if interval > 0 {
		globalConfig.IntervalMs = interval
	}
	if payload > 0 {
		globalConfig.PayloadSize = payload
	}
	if verbose {
		globalConfig.VerboseReport = verbose
	}

	task := &boomer.Task{
		Name:   "udp_echo_task",
		Weight: 10,
		Fn:     udpEchoTask,
	}

	// 设置 Locust Master 地址和端口 (通过 flag 包设置，以覆盖 boomer 的默认值)
	// boomer 在加载时会注册这些 flag
	flag.Set("master-host", globalConfig.LocustAddr)
	flag.Set("master-port", "5557")

	fmt.Printf("Starting Boomer client (Locust Master: %s, Interval: %dms, Payload: %dB)\n",
		globalConfig.LocustAddr, globalConfig.IntervalMs, globalConfig.PayloadSize)
	fmt.Printf("Warp Range: %s [%d~%d], Server Range: %s [%d~%d]\n",
		globalConfig.WarpIP, globalConfig.WarpPortMin, globalConfig.WarpPortMax,
		globalConfig.ServerIP, globalConfig.ServerPortMin, globalConfig.ServerPortMax)

	// 使用全局 Run 方法，它内部包含了阻塞逻辑和信号处理
	boomer.Run(task)
}

func udpEchoTask() {
	// 1. 实现双端轮询
	// 1.1 轮询 Warp 端口
	warpPort := globalConfig.WarpPortMin
	if globalConfig.WarpPortMax > globalConfig.WarpPortMin {
		count := atomic.AddUint32(&warpPortCounter, 1)
		warpPort = globalConfig.WarpPortMin + int((count-1)%uint32(globalConfig.WarpPortMax-globalConfig.WarpPortMin+1))
	}
	warpAddr := fmt.Sprintf("%s:%d", globalConfig.WarpIP, warpPort)

	// 1.2 轮询 Server 端口 (用于握手负载)
	serverPort := globalConfig.ServerPortMin
	if globalConfig.ServerPortMax > globalConfig.ServerPortMin {
		count := atomic.AddUint32(&serverPortCounter, 1)
		serverPort = globalConfig.ServerPortMin + int((count-1)%uint32(globalConfig.ServerPortMax-globalConfig.ServerPortMin+1))
	}
	serverAddr := fmt.Sprintf("%s:%d", globalConfig.ServerIP, serverPort)

	conn, err := net.Dial("udp", warpAddr)
	if err != nil {
		boomer.RecordFailure("udp", "dial_handshake", 0, err.Error())
		return
	}

	// 确保最终关闭当前连接
	var activeConn net.Conn = conn
	defer func() {
		if activeConn != nil {
			activeConn.Close()
		}
	}()

	// 获取本地 IP (不带端口)
	localHost, _, _ := net.SplitHostPort(activeConn.LocalAddr().String())

	// 定义名称装饰逻辑
	// 初始状态下不加后缀，待握手成功确认模式后再决定
	enhancedSuffix := ""
	statName := func(base string) string {
		if base == "echo" && enhancedSuffix != "" && globalConfig.VerboseReport {
			return fmt.Sprintf("%s %s", base, enhancedSuffix)
		}
		return base
	}

	handshakePacket := make([]byte, 1024)
	handshakePacket[0] = TypeHandshake
	// Session ID = 0 (1-4)
	addrBytes := []byte(serverAddr)
	handshakePacket[5] = uint8(len(addrBytes))
	copy(handshakePacket[6:], addrBytes)

	start := time.Now()
	_, err = activeConn.Write(handshakePacket)
	if err != nil {
		boomer.RecordFailure("udp", statName("write_handshake"), 0, err.Error())
		return
	}

	activeConn.SetReadDeadline(time.Now().Add(1 * time.Second))
	respBuf := make([]byte, 2048)
	n, err := activeConn.Read(respBuf)
	if err != nil {
		boomer.RecordFailure("udp", statName("read_handshake"), time.Since(start).Nanoseconds()/1e6, err.Error())
		return
	}

	if n < 5 || respBuf[0] != TypeReply {
		boomer.RecordFailure("udp", statName("invalid_handshake_resp"), time.Since(start).Nanoseconds()/1e6, "invalid response")
		return
	}

	sessionID := binary.BigEndian.Uint32(respBuf[1:5])

	// 如果响应长度为 7，说明包含动态端口，自动切换模式
	if n == 7 {
		dynamicPort := binary.BigEndian.Uint16(respBuf[5:7])
		host, _, _ := net.SplitHostPort(warpAddr)
		newTargetAddr := fmt.Sprintf("%s:%d", host, dynamicPort)
		fmt.Printf("Session %d: Switching to dynamic port on Warp: %s\n", sessionID, newTargetAddr)

		// 关闭旧连接，连接到新端口
		activeConn.Close()
		newConn, err := net.Dial("udp", newTargetAddr)
		if err != nil {
			boomer.RecordFailure("udp", statName("dial_dynamic"), 0, err.Error())
			activeConn = nil // 防止 defer 再次关闭
			return
		}
		activeConn = newConn
		// 动态模式不启用增强后缀
		enhancedSuffix = ""
	} else if n == 5 {
		// 固定模式且是 echo 时增加信息 (本地IP -> WarpIP:端口)
		enhancedSuffix = fmt.Sprintf("(%s -> %s)", localHost, warpAddr)
	}

	boomer.RecordSuccess("udp", statName("handshake"), time.Since(start).Nanoseconds()/1e6, int64(n))

	// 2. Data Communication (Precise RTT & SN)
	// 使用并发模型：一个 goroutine 发送，当前 goroutine 接收
	// 这样可以实现定时发送和更精确的 RTT 测量

	sendCount := uint32(0)
	recvCount := uint32(0)
	interval := time.Duration(globalConfig.IntervalMs) * time.Millisecond // 发送间隔
	maxPackets := uint32(600000 / globalConfig.IntervalMs)               // 10min / interval

	stopChan := make(chan struct{})

	// 启动接收协程
	go func() {
		defer close(stopChan)
		readBuf := make([]byte, 2048)

		for recvCount < maxPackets {
			activeConn.SetReadDeadline(time.Now().Add(1 * time.Second)) // 每次读取设置1秒超时
			n, err := activeConn.Read(readBuf)
			if err != nil {
				// 如果是超时且还没发够，继续等待（防止偶尔的抖动）；如果连续失败或发完则退出
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					if sendCount < maxPackets {
						continue
					}
				}
				break
			}

			if n < 17 {
				continue
			}

			// 解析报头
			// respType := readBuf[0]
			respSessionID := binary.BigEndian.Uint32(readBuf[1:5])
			if respSessionID != sessionID {
				continue
			}

			// respSN := binary.BigEndian.Uint32(readBuf[5:9])
			respTS := int64(binary.BigEndian.Uint64(readBuf[9:17]))

			// 计算 RTT
			now := time.Now().UnixNano()
			rttMs := float64(now-respTS) / 1e6

			boomer.RecordSuccess("udp", statName("echo"), int64(rttMs), int64(n))
			recvCount++
		}
	}()

	// 发送循环
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := uint32(0); i < maxPackets; i++ {
		packet := make([]byte, 17+globalConfig.PayloadSize)
		packet[0] = TypeData
		binary.BigEndian.PutUint32(packet[1:5], sessionID)
		binary.BigEndian.PutUint32(packet[5:9], i)
		binary.BigEndian.PutUint64(packet[9:17], uint64(time.Now().UnixNano()))

		_, err := activeConn.Write(packet)
		if err != nil {
			boomer.RecordFailure("udp", statName("write_data"), 0, err.Error())
		}
		sendCount++

		select {
		case <-ticker.C:
		case <-stopChan:
			goto END
		}
	}

	// 等待接收协程完成或者超时
	<-stopChan

END:
	if recvCount < sendCount {
		loss := sendCount - recvCount
		boomer.RecordFailure("udp", statName("packet_loss"), 0, fmt.Sprintf("lost %d packets", loss))
	}
}
