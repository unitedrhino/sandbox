#!/usr/bin/env python3
"""
原始 socket 绕过 iptables 测试
验证 SOCK_RAW + IPPROTO_TCP/UDP/ICMP 是否能绕过 OUTPUT DROP

运行方式（需要 root + NET_ADMIN + NET_RAW）：
    sudo python3 test_raw_socket_bypass.py
"""

import socket
import struct
import random
import time
import os
import sys
import subprocess

TARGET_IP = "34.197.4.100"  # httpbin.org
TARGET_HOST = "httpbin.org"
PROXY_IP = "10.233.1.2"
PROXY_PORT = 1080

PASS = 0
FAIL = 0


def log_pass(msg):
    global PASS
    PASS += 1
    print(f"\033[0;32m[PASS]\033[0m {msg}")


def log_fail(msg):
    global FAIL
    FAIL += 1
    print(f"\033[0;31m[FAIL]\033[0m {msg}")


def log_info(msg):
    print(f"\033[1;33m[INFO]\033[0m {msg}")


def setup_iptables():
    """配置 claw 模拟的 iptables 规则"""
    log_info("配置 iptables 规则...")
    cmds = [
        ["iptables", "-w", "-F", "OUTPUT"],
        ["iptables", "-w", "-P", "OUTPUT", "DROP"],
        ["iptables", "-w", "-A", "OUTPUT", "-o", "lo", "-j", "ACCEPT"],
        ["iptables", "-w", "-A", "OUTPUT", "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"],
        ["iptables", "-w", "-A", "OUTPUT", "-d", PROXY_IP, "-p", "tcp", "--dport", str(PROXY_PORT), "-j", "ACCEPT"],
        ["iptables", "-w", "-A", "OUTPUT", "-p", "udp", "-j", "DROP"],
    ]
    for cmd in cmds:
        subprocess.run(cmd, check=True)
    subprocess.run(["iptables", "-w", "-L", "OUTPUT", "-v", "-n"])


def cleanup_iptables():
    log_info("清理 iptables 规则...")
    subprocess.run(["iptables", "-w", "-F", "OUTPUT"], check=False)
    subprocess.run(["iptables", "-w", "-P", "OUTPUT", "ACCEPT"], check=False)


# ========== 测试 1：原始 ICMP socket ==========
def test_raw_icmp():
    """测试 SOCK_RAW + IPPROTO_ICMP 是否能绕过 OUTPUT DROP"""
    print("\n========== 测试 1：原始 ICMP socket（ping）==========")

    try:
        # 创建原始 ICMP socket
        sock = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_ICMP)
    except PermissionError:
        log_pass("原始 ICMP socket 创建被拒绝（权限不足）")
        return
    except Exception as e:
        log_fail(f"原始 ICMP socket 创建失败: {e}")
        return

    # 构造 ICMP Echo Request
    icmp_id = random.randint(0, 65535)
    icmp_seq = 1
    icmp_header = struct.pack("!BBHHH", 8, 0, 0, icmp_id, icmp_seq)  # type=8, code=0
    # 计算 checksum
    def checksum(data):
        if len(data) % 2:
            data += b'\x00'
        s = sum(struct.unpack("!" + "H" * (len(data) // 2), data))
        s = (s >> 16) + (s & 0xffff)
        s += s >> 16
        return ~s & 0xffff

    icmp_checksum = checksum(icmp_header + b'ABCDEFGHIJKLMNOPQRSTUVWXYZ')
    icmp_header = struct.pack("!BBHHH", 8, 0, icmp_checksum, icmp_id, icmp_seq)
    payload = b'ABCDEFGHIJKLMNOPQRSTUVWXYZ'

    try:
        sock.settimeout(3)
        sock.sendto(icmp_header + payload, (TARGET_IP, 0))
        log_info("ICMP Echo Request 已发送，等待回复...")

        try:
            data, addr = sock.recvfrom(1024)
            # 原始 socket 收到的数据包含 IP 头
            ip_header_len = (data[0] & 0x0f) * 4
            icmp_reply = data[ip_header_len:]
            reply_type = icmp_reply[0]
            reply_id = struct.unpack("!H", icmp_reply[4:6])[0]

            if reply_type == 0 and reply_id == icmp_id:
                log_fail("收到 ICMP Echo Reply！原始 socket 绕过了 OUTPUT DROP")
            else:
                log_info(f"收到非预期 ICMP 包: type={reply_type}, id={reply_id}")
        except socket.timeout:
            log_pass("ICMP 无回复（被 OUTPUT DROP 拦截）")
        except Exception as e:
            log_info(f"ICMP recvfrom 异常: {e}")
    except PermissionError:
        log_pass("ICMP sendto 被拒绝")
    except OSError as e:
        # EACCES 或 EPERM 表示被防火墙拦截
        if e.errno in (1, 13):  # EPERM, EACCES
            log_pass(f"ICMP sendto 被内核拒绝 (errno={e.errno})，iptables 生效")
        else:
            log_info(f"ICMP sendto 失败: {e}")
    except Exception as e:
        log_info(f"ICMP sendto 异常: {e}")
    finally:
        sock.close()


# ========== 测试 2：原始 TCP socket（构造 SYN）=========
def test_raw_tcp_syn():
    """测试 SOCK_RAW + IPPROTO_TCP 是否能构造 SYN 包绕过 OUTPUT DROP"""
    print("\n========== 测试 2：原始 TCP socket（SYN）==========")

    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_TCP)
    except PermissionError:
        log_pass("原始 TCP socket 创建被拒绝")
        return
    except Exception as e:
        log_fail(f"原始 TCP socket 创建失败: {e}")
        return

    try:
        # 开启 IP_HDRINCL，让内核处理 IP 头
        sock.setsockopt(socket.IPPROTO_IP, socket.IP_HDRINCL, 1)
    except Exception as e:
        log_info(f"IP_HDRINCL 设置失败: {e}")

    # 构造 IP 头 + TCP SYN 包
    src_port = random.randint(40000, 60000)
    seq_num = random.randint(0, 0xffffffff)

    # IP 头 (20 bytes)
    ip_ihl = 5
    ip_ver = 4
    ip_tos = 0
    ip_tot_len = 20 + 20  # IP头 + TCP头
    ip_id = random.randint(0, 65535)
    ip_frag_off = 0
    ip_ttl = 64
    ip_proto = socket.IPPROTO_TCP
    ip_check = 0
    ip_saddr = socket.inet_aton("0.0.0.0")  # 内核会填充
    ip_daddr = socket.inet_aton(TARGET_IP)

    ip_header = struct.pack("!BBHHHBBH4s4s",
        (ip_ver << 4) + ip_ihl, ip_tos, ip_tot_len,
        ip_id, ip_frag_off, ip_ttl, ip_proto, ip_check,
        ip_saddr, ip_daddr)

    # TCP 头 (20 bytes，无选项)
    tcp_offset = 5 << 4  # 数据偏移 (5 * 4 = 20 bytes)
    tcp_flags = 0x02  # SYN
    tcp_window = socket.htons(5840)
    tcp_check = 0
    tcp_urg_ptr = 0

    tcp_header = struct.pack("!HHLLBBHHH",
        src_port, 80, seq_num, 0,
        tcp_offset, tcp_flags, tcp_window, tcp_check, tcp_urg_ptr)

    # 计算 TCP checksum（伪头部 + TCP 头）
    def tcp_checksum(src, dst, proto, tcp_hdr, payload=b''):
        pseudo = struct.pack("!4s4sBBH", src, dst, 0, proto, len(tcp_hdr) + len(payload))
        data = pseudo + tcp_hdr + payload
        if len(data) % 2:
            data += b'\x00'
        s = sum(struct.unpack("!" + "H" * (len(data) // 2), data))
        s = (s >> 16) + (s & 0xffff)
        s += s >> 16
        return ~s & 0xffff

    tcp_check = tcp_checksum(ip_saddr, ip_daddr, socket.IPPROTO_TCP, tcp_header)
    tcp_header = struct.pack("!HHLLBBHHH",
        src_port, 80, seq_num, 0,
        tcp_offset, tcp_flags, tcp_window, tcp_check, tcp_urg_ptr)

    packet = ip_header + tcp_header

    try:
        sock.settimeout(3)
        sock.sendto(packet, (TARGET_IP, 0))
        log_info("TCP SYN 包已发送，等待 SYN-ACK...")

        try:
            data, addr = sock.recvfrom(4096)
            ip_header_len = (data[0] & 0x0f) * 4
            tcp_reply = data[ip_header_len:]
            reply_flags = tcp_reply[13]
            if reply_flags & 0x12:  # SYN + ACK
                log_fail("收到 SYN-ACK！原始 TCP socket 绕过了 OUTPUT DROP")
            elif reply_flags & 0x04:  # RST
                log_info("收到 RST（连接被拒绝，但包通过了 OUTPUT）")
            else:
                log_info(f"收到非预期 TCP 包: flags={reply_flags}")
        except socket.timeout:
            log_pass("TCP SYN 无回复（被 OUTPUT DROP 拦截）")
        except Exception as e:
            log_info(f"TCP recvfrom 异常: {e}")
    except PermissionError:
        log_pass("TCP SYN sendto 被拒绝")
    except OSError as e:
        if e.errno in (1, 13):
            log_pass(f"TCP SYN sendto 被内核拒绝 (errno={e.errno})，iptables 生效")
        else:
            log_info(f"TCP SYN sendto 失败: {e}")
    except Exception as e:
        log_info(f"TCP SYN sendto 异常: {e}")
    finally:
        sock.close()


# ========== 测试 3：SOCK_STREAM + IP_HDRINCL ==========
def test_stream_hdrincl():
    """测试 SOCK_STREAM + IP_HDRINCL 是否能绕过"""
    print("\n========== 测试 3：SOCK_STREAM + IP_HDRINCL ==========")

    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM, socket.IPPROTO_TCP)
        sock.setsockopt(socket.IPPROTO_IP, socket.IP_HDRINCL, 1)
        log_info("SOCK_STREAM + IP_HDRINCL 创建成功")
    except (PermissionError, OSError) as e:
        log_pass(f"SOCK_STREAM + IP_HDRINCL 创建失败: {e}")
        return
    except Exception as e:
        log_info(f"SOCK_STREAM + IP_HDRINCL 异常: {e}")
        return

    # 尝试 connect
    try:
        sock.settimeout(3)
        sock.connect((TARGET_IP, 80))
        log_fail("SOCK_STREAM + IP_HDRINCL connect 成功（绕过 OUTPUT DROP）")
    except PermissionError:
        log_pass("connect 被拒绝")
    except OSError as e:
        if e.errno in (1, 13, 111):  # EPERM, EACCES, ECONNREFUSED
            log_pass(f"connect 被拦截 (errno={e.errno})")
        else:
            log_info(f"connect 失败: {e}")
    except socket.timeout:
        log_pass("connect 超时（被 OUTPUT DROP 拦截）")
    except Exception as e:
        log_info(f"connect 异常: {e}")
    finally:
        sock.close()


# ========== 测试 4：SOCK_DGRAM + IPPROTO_RAW ==========
def test_dgram_raw():
    """测试 SOCK_DGRAM + IPPROTO_RAW"""
    print("\n========== 测试 4：SOCK_DGRAM + IPPROTO_RAW ==========")

    try:
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM, socket.IPPROTO_RAW)
        log_info("SOCK_DGRAM + IPPROTO_RAW 创建成功")
    except (PermissionError, OSError) as e:
        log_pass(f"SOCK_DGRAM + IPPROTO_RAW 创建失败: {e}")
        return

    try:
        sock.settimeout(3)
        sock.sendto(b"TEST", (TARGET_IP, 0))
        log_fail("SOCK_DGRAM + IPPROTO_RAW 发送成功（绕过 OUTPUT DROP）")
    except PermissionError:
        log_pass("sendto 被拒绝")
    except OSError as e:
        if e.errno in (1, 13):
            log_pass(f"sendto 被内核拒绝 (errno={e.errno})")
        else:
            log_info(f"sendto 失败: {e}")
    except Exception as e:
        log_info(f"sendto 异常: {e}")
    finally:
        sock.close()


# ========== 测试 5：检查 /proc/net/raw ==========
def test_proc_net_raw():
    """检查系统中是否有其他原始 socket 在运行"""
    print("\n========== 测试 5：/proc/net/raw 状态 ==========")
    try:
        with open("/proc/net/raw", "r") as f:
            lines = f.readlines()
            # 跳过表头，统计原始 socket 数量
            count = len(lines) - 1
            log_info(f"系统中当前有 {count} 个原始 socket")
    except Exception as e:
        log_info(f"无法读取 /proc/net/raw: {e}")


# ========== 主函数 ==========
def main():
    if os.geteuid() != 0:
        print("错误：需要 root 权限运行")
        sys.exit(1)

    log_info(f"测试目标: {TARGET_HOST} ({TARGET_IP})")
    log_info(f"当前 capability: {os.getuid()}/{os.getgid()}")

    # 检查是否有 NET_RAW
    try:
        with open("/proc/self/status", "r") as f:
            for line in f:
                if line.startswith("CapEff:"):
                    cap_eff = int(line.split()[1], 16)
                    log_info(f"有效 capability: 0x{cap_eff:x}")
                    if cap_eff & (1 << 13):  # CAP_NET_RAW = 13
                        log_info("CAP_NET_RAW: 已启用")
                    else:
                        log_info("CAP_NET_RAW: 未启用")
                    break
    except Exception:
        pass

    setup_iptables()

    try:
        test_raw_icmp()
        test_raw_tcp_syn()
        test_stream_hdrincl()
        test_dgram_raw()
        test_proc_net_raw()
    finally:
        cleanup_iptables()

    print("\n========== 测试结果汇总 ==========")
    print(f"通过: \033[0;32m{PASS}\033[0m")
    print(f"失败: \033[0;31m{FAIL}\033[0m")

    if FAIL > 0:
        print(f"\n\033[0;31m警告：发现 {FAIL} 个可绕过路径！\033[0m")
        sys.exit(1)
    else:
        print(f"\n\033[0;32m原始 socket 无法绕过 OUTPUT DROP。\033[0m")
        sys.exit(0)


if __name__ == "__main__":
    main()
