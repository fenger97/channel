#!/bin/bash

# 编译脚本：构建 server 与 client_boomer
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 二进制输出目录（已加入 .gitignore，不提交）
BIN_DIR="./bin"
mkdir -p "$BIN_DIR"

SERVER_BIN="$BIN_DIR/server"
CLIENT_BIN="$BIN_DIR/client_boomer"
BENCH_BIN="$BIN_DIR/bench_buffer"

usage() {
    echo "用法: $0 [all|server|client|bench]"
    echo "  all    : 编译 server、client_boomer、bench_buffer (默认)"
    echo "  server : 仅编译 server"
    echo "  client : 仅编译 client_boomer"
    echo "  bench  : 仅编译 bench_buffer（环形缓冲 vs channel 压测）"
    exit 1
}

build_server() {
    echo "正在编译 server..."
    go build -o "$SERVER_BIN" ./cmd/server
    echo "  -> $SERVER_BIN"
    echo "  启动示例: $SERVER_BIN -mode fixed -queue channel (需在项目根目录执行)"
}

build_client() {
    echo "正在编译 client_boomer..."
    go build -o "$CLIENT_BIN" ./cmd/client_boomer
    echo "  -> $CLIENT_BIN"
}

build_bench() {
    echo "正在编译 bench_buffer..."
    go build -o "$BENCH_BIN" ./cmd/bench_buffer
    echo "  -> $BENCH_BIN"
}

case "${1:-all}" in
    all)
        build_server
        build_client
        build_bench
        echo "全部编译完成。"
        ;;
    server)
        build_server
        ;;
    client)
        build_client
        ;;
    bench)
        build_bench
        ;;
    *)
        usage
        ;;
esac
