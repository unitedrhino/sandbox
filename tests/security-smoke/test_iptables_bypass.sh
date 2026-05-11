#!/bin/bash
# iptables 网络隔离绕过测试
# 模拟 claw sandbox 子进程的网络环境，验证各种绕过路径
#
# 运行方式（需要 root + NET_ADMIN）：
#   sudo ./test_iptables_bypass.sh
#
# 测试目标：验证 iptables OUTPUT DROP + 只允许 SOCKS5 代理 的隔离强度

set -euo pipefail

PROXY_IP="10.233.1.2"
PROXY_PORT="1080"
TARGET_HOST="httpbin.org"
TARGET_IP=""
PASS=0
FAIL=0

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

log_pass() { echo -e "${GREEN}[PASS]${NC} $1"; PASS=$((PASS + 1)); }
log_fail() { echo -e "${RED}[FAIL]${NC} $1"; FAIL=$((FAIL + 1)); }
log_info() { echo -e "${YELLOW}[INFO]${NC} $1"; }

cleanup() {
    log_info "清理 iptables 规则..."
    iptables -w -F OUTPUT 2>/dev/null || true
    iptables -w -P OUTPUT ACCEPT 2>/dev/null || true
}
trap cleanup EXIT

# ========== 前置检查 ==========
if [ "$EUID" -ne 0 ]; then
    echo "错误：需要 root 权限运行"
    exit 1
fi

if ! command -v curl &>/dev/null; then
    echo "错误：需要 curl"
    exit 1
fi

# 解析目标 IP
TARGET_IP=$(dig +short "$TARGET_HOST" | head -1)
if [ -z "$TARGET_IP" ]; then
    TARGET_IP="3.214.12.120"  # httpbin.org 的 fallback IP
fi
log_info "测试目标: $TARGET_HOST ($TARGET_IP)"

# ========== 步骤 1：基准测试（无限制） ==========
echo ""
echo "========== 基准测试（无 iptables 限制）=========="

if curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 "http://$TARGET_HOST/get" | grep -q "200"; then
    log_pass "基准访问成功（无限制时可访问外网）"
else
    log_fail "基准访问失败（网络不通，跳过后续测试）"
    exit 1
fi

# ========== 步骤 2：配置 claw 模拟规则 ==========
echo ""
echo "========== 配置 sandbox 网络隔离规则 =========="

iptables -w -F OUTPUT
iptables -w -P OUTPUT DROP
iptables -w -A OUTPUT -o lo -j ACCEPT
iptables -w -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -w -A OUTPUT -d "$PROXY_IP" -p tcp --dport "$PROXY_PORT" -j ACCEPT
iptables -w -A OUTPUT -p udp -j DROP

log_info "iptables 规则已配置："
iptables -w -L OUTPUT -v -n --line-numbers

# ========== 步骤 3：正常访问测试（应被阻止） ==========
echo ""
echo "========== 测试 1：直接 HTTP 访问（应被阻止）=========="

if curl -s -o /dev/null -w "%{http_code}" --connect-timeout 3 "http://$TARGET_HOST/get" 2>/dev/null | grep -q "200"; then
    log_fail "直接 HTTP 访问成功（隔离失效！）"
else
    log_pass "直接 HTTP 访问被阻止"
fi

# ========== 步骤 4：绕过 1 - 清空 iptables ==========
echo ""
echo "========== 测试 2：清空 iptables OUTPUT 链（绕过尝试）=========="

iptables -w -F OUTPUT
iptables -w -P OUTPUT ACCEPT

if curl -s -o /dev/null -w "%{http_code}" --connect-timeout 3 "http://$TARGET_HOST/get" 2>/dev/null | grep -q "200"; then
    log_fail "清空 iptables 后访问成功（绕过可行！）"
else
    log_pass "清空 iptables 后仍无法访问（意外）"
fi

# 恢复规则
iptables -w -F OUTPUT
iptables -w -P OUTPUT DROP
iptables -w -A OUTPUT -o lo -j ACCEPT
iptables -w -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
iptables -w -A OUTPUT -d "$PROXY_IP" -p tcp --dport "$PROXY_PORT" -j ACCEPT
iptables -w -A OUTPUT -p udp -j DROP

# ========== 步骤 5：绕过 2 - 添加放行规则 ==========
echo ""
echo "========== 测试 3：添加自定义放行规则（绕过尝试）=========="

iptables -w -A OUTPUT -d "$TARGET_IP" -p tcp --dport 80 -j ACCEPT

if curl -s -o /dev/null -w "%{http_code}" --connect-timeout 3 "http://$TARGET_HOST/get" 2>/dev/null | grep -q "200"; then
    log_fail "添加放行规则后访问成功（绕过可行！）"
else
    log_pass "添加放行规则后仍无法访问（意外）"
fi

# 恢复规则
iptables -w -D OUTPUT -d "$TARGET_IP" -p tcp --dport 80 -j ACCEPT 2>/dev/null || true

# ========== 步骤 6：绕过 3 - UDP 直连 ==========
echo ""
echo "========== 测试 4：UDP 直连（应被阻止）=========="

if command -v dig &>/dev/null; then
    if dig +short +time=2 @8.8.8.8 google.com &>/dev/null; then
        log_fail "UDP DNS 查询成功（UDP 隔离失效！）"
    else
        log_pass "UDP DNS 查询被阻止"
    fi
else
    log_info "跳过 UDP 测试（无 dig 命令）"
fi

# 用 nc 测试 UDP
if command -v nc &>/dev/null; then
    if timeout 2 nc -u -z -w 2 8.8.8.8 53 2>/dev/null; then
        log_fail "UDP 53 端口连通（UDP 隔离失效！）"
    else
        log_pass "UDP 53 端口被阻止"
    fi
fi

# ========== 步骤 7：绕过 4 - ICMP (ping) ==========
echo ""
echo "========== 测试 5：ICMP ping（应被阻止）=========="

if ping -c 1 -W 2 "$TARGET_IP" &>/dev/null; then
    log_fail "ICMP ping 成功（ICMP 未被隔离）"
else
    log_pass "ICMP ping 被阻止"
fi

# ========== 步骤 8：绕过 5 - 原始 socket ==========
echo ""
echo "========== 测试 6：原始 socket（绕过尝试）=========="

if command -v python3 &>/dev/null; then
    PYTHON_TEST=$(python3 -c "
import socket
import sys
try:
    s = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_TCP)
    s.close()
    print('RAW_OK')
except PermissionError:
    print('RAW_DENIED')
except Exception as e:
    print(f'RAW_ERROR:{e}')
" 2>/dev/null)
    
    if echo "$PYTHON_TEST" | grep -q "RAW_OK"; then
        log_fail "原始 socket 可创建（需进一步测试是否可绕过 iptables）"
    elif echo "$PYTHON_TEST" | grep -q "RAW_DENIED"; then
        log_pass "原始 socket 被权限拒绝"
    else
        log_info "原始 socket 测试结果: $PYTHON_TEST"
    fi
else
    log_info "跳过原始 socket 测试（无 python3）"
fi

# ========== 汇总 ==========
echo ""
echo "========== 测试结果汇总 =========="
echo -e "通过: ${GREEN}$PASS${NC}"
echo -e "失败: ${RED}$FAIL${NC}"

if [ "$FAIL" -gt 0 ]; then
    echo ""
    echo -e "${RED}警告：发现 $FAIL 个可绕过路径，需要加固！${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}当前 iptables 规则无已知绕过路径。${NC}"
    exit 0
fi
