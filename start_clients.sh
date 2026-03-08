#!/bin/bash

# 配置部分（与 build.sh 一致：二进制输出到 bin/）
BIN_DIR="./bin"
CLIENT_BIN="$BIN_DIR/client_boomer"
CONFIG_FILE="config.json"
DEFAULT_NUM_CLIENTS=5
LOG_DIR="logs"

usage() {
    echo "用法: $0 {start|stop|restart} [数量]"
    echo "  start [数量]   : 启动指定数量的客户端 (默认: $DEFAULT_NUM_CLIENTS)"
    echo "  stop           : 停止所有运行中的客户端"
    echo "  restart [数量] : 先停止再启动，可选指定数量 (默认: $DEFAULT_NUM_CLIENTS)"
    exit 1
}

start_clients() {
    num_clients=${1:-$DEFAULT_NUM_CLIENTS}
    mkdir -p $LOG_DIR
    
    echo "正在启动 $num_clients 个客户端..."
    for i in $(seq 1 $num_clients); do
        log_file="$LOG_DIR/client_$i.log"
        # 使用 nohup 后台运行
        nohup $CLIENT_BIN -config $CONFIG_FILE > "$log_file" 2>&1 &
        echo "客户端 $i 已启动 (PID: $!, 日志: $log_file)"
    done
    echo "所有客户端已启动。"
}

stop_clients() {
    echo "正在停止所有客户端..."
    # 查找并杀死进程
    pids=$(pgrep -f "$CLIENT_BIN -config $CONFIG_FILE")
    if [ -z "$pids" ]; then
        echo "没有发现正在运行的客户端。"
    else
        kill $pids
        echo "已发送停止信号给以下进程: $pids"
        sleep 1
        # 再次检查
        pids=$(pgrep -f "$CLIENT_BIN -config $CONFIG_FILE")
        if [ ! -z "$pids" ]; then
            echo "强制结束剩余进程: $pids"
            kill -9 $pids
        fi
        echo "清理完毕。"
    fi
}

# 主处理逻辑
case "$1" in
    start)
        # 确保二进制文件存在且可执行
        if [ ! -f "$CLIENT_BIN" ]; then
            echo "错误: 未找到 $CLIENT_BIN，正在尝试编译..."
            mkdir -p "$BIN_DIR"
            go build -o "$CLIENT_BIN" ./cmd/client_boomer
        fi
        start_clients "$2"
        ;;
    stop)
        stop_clients
        ;;
    restart)
        stop_clients
        sleep 1
        if [ ! -f "$CLIENT_BIN" ]; then
            echo "正在编译 $CLIENT_BIN..."
            mkdir -p "$BIN_DIR"
            go build -o "$CLIENT_BIN" ./cmd/client_boomer
        fi
        start_clients "$2"
        ;;
    *)
        usage
        ;;
esac
