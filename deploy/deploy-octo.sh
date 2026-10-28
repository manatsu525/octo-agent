#!/usr/bin/env bash
# =============================================================================
#  octo-agent 一键部署脚本（Debian 11/12/13，x86_64 / arm64，不使用 Docker）
#
#  做的事：
#    1. 安装依赖：curl tar ca-certificates openssl git ripgrep nginx(-light)
#    2. 内存小于 2G 时把 swap 补到约 2G
#    3. 从镜像仓库 manatsu525/octo-agent 安装 octo（校验 SHA-256），失败回退官方脚本
#    4. 创建独立的非 root 用户 octo 运行服务（agent 能跑命令，别给它 root）
#    5. octo 只监听 127.0.0.1:8088，由 nginx 在公网端口提供自签名 HTTPS
#    6. 生成随机访问密钥，systemd 托管、开机自启
#    7. 加一层账号密码登录页（安装时交互输入管理员账号和密码）
#    8. 预置两个免 key 的搜索 MCP：Keenable、Parallel（已有的 mcp.json 不覆盖）
#
#  用法（root）：
#    bash deploy-octo.sh              安装 / 升级 / 改登录密码
#    bash deploy-octo.sh uninstall    卸载（会询问是否连聊天数据一起删）
#  可选环境变量：
#    OCTO_PORT=8557        对外 HTTPS 端口
#    OCTO_USER=octo        运行 octo 的系统用户
#    OCTO_HOST=1.2.3.4     证书里写的 IP/域名（默认自动探测公网 IP）
#    OCTO_ACCESS_KEY=xxx   自定义访问密钥（默认随机生成；重复运行会沿用旧的）
#    OCTO_ADMIN_USER / OCTO_ADMIN_PASS  免交互指定登录账号密码（不设则安装时询问）
#    SWAP_TARGET_MB=2048   swap 目标总量，0 表示不动 swap
#    OCTO_VERSION=1.17.21-m1  指定 octo 版本（默认镜像仓库最新 Release；-m1 为手机回车换行补丁版）
#    OCTO_UNINSTALL_YES=1 / OCTO_PURGE=1|0  卸载时免确认 / 是否删除数据
#    OCTO_REPO=owner/repo  下载 octo 的 GitHub 仓库（默认 manatsu525/octo-agent）
#
#  重复运行是安全的：会升级 octo、保留数据和密钥、重写 nginx/systemd 配置。
# =============================================================================
set -euo pipefail

OCTO_PORT="${OCTO_PORT:-8557}"
OCTO_USER="${OCTO_USER:-octo}"
OCTO_REPO="${OCTO_REPO:-manatsu525/octo-agent}"
SWAP_TARGET_MB="${SWAP_TARGET_MB:-2048}"
INTERNAL_ADDR="127.0.0.1:8088"

log()  { printf '\033[1;32m[octo-deploy]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[octo-deploy]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[octo-deploy]\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" -eq 0 ] || die "请用 root 运行"
[ -f /etc/debian_version ] || die "只支持 Debian 系统"
command -v systemctl >/dev/null || die "需要 systemd"

# ---------------------------------------------------------------- 卸载
if [ "${1:-}" = "uninstall" ]; then
  ask() { # ask <提示> <默认Y/N>，非交互时取默认
    local a; [ -r /dev/tty ] && read -r -p "$1" a </dev/tty 2>/dev/null || a=""
    case "${a:-$2}" in [Yy]*) return 0 ;; *) return 1 ;; esac
  }
  if [ "${OCTO_UNINSTALL_YES:-}" != 1 ]; then
    ask "确认卸载 octo（服务、登录页、nginx 配置、证书、swap 文件）？[y/N] " N || die "已取消"
  fi
  OCTO_HOME=$(getent passwd "$OCTO_USER" | cut -d: -f6 || true)
  PURGE="${OCTO_PURGE:-}"
  if [ -z "$PURGE" ] && [ -n "$OCTO_HOME" ]; then
    ask "同时删除聊天记录、配置和工作目录（$OCTO_HOME）？删了无法恢复 [y/N] " N && PURGE=1 || PURGE=0
  fi
  log "停止服务..."
  for s in octo octo-auth; do
    systemctl disable --now "$s" >/dev/null 2>&1 || true
    rm -f "/etc/systemd/system/$s.service"
  done
  systemctl daemon-reload
  rm -f /usr/local/bin/octo /usr/local/bin/octo-auth
  rm -rf /etc/octo-auth
  id octo-auth >/dev/null 2>&1 && userdel octo-auth 2>/dev/null || true

  log "清理 nginx 配置和证书..."
  rm -f /etc/nginx/conf.d/octo.conf
  rm -rf /etc/nginx/octo-ssl
  if [ -z "$(ls -A /etc/nginx/conf.d /etc/nginx/sites-enabled 2>/dev/null)" ]; then
    systemctl disable --now nginx >/dev/null 2>&1 || true   # 没有别的站点就停掉 nginx（软件包保留）
  elif nginx -t -q 2>/dev/null; then
    systemctl reload nginx || true
  fi
  if command -v ufw >/dev/null && ufw status | grep -q active; then
    ufw delete allow "$OCTO_PORT/tcp" >/dev/null 2>&1 || true
  fi

  if [ -f /swapfile.octo ]; then
    log "移除 /swapfile.octo..."
    swapoff /swapfile.octo 2>/dev/null || warn "swap 正在使用中无法关闭，已保留 /swapfile.octo"
    if ! swapon --show=NAME --noheadings | grep -q '/swapfile.octo'; then
      rm -f /swapfile.octo
      sed -i '\#/swapfile.octo#d' /etc/fstab
    fi
  fi
  rm -f /etc/sysctl.d/90-octo-swap.conf

  if [ "$PURGE" = 1 ] && id "$OCTO_USER" >/dev/null 2>&1; then
    log "删除用户 $OCTO_USER 及其数据..."
    pkill -u "$OCTO_USER" 2>/dev/null || true; sleep 1
    userdel -r "$OCTO_USER" 2>/dev/null || true
  elif [ -n "$OCTO_HOME" ]; then
    log "已保留用户 $OCTO_USER 和数据目录 $OCTO_HOME（重新安装会接着用）"
  fi
  log "卸载完成。依赖软件包（nginx、git 等）未删除。"
  exit 0
fi

# 端口冲突检查（8557 被别的程序占用就停下，不去动它）
if ss -ltnH "sport = :$OCTO_PORT" 2>/dev/null | grep -q . ; then
  if ! ss -ltnpH "sport = :$OCTO_PORT" | grep -q nginx; then
    die "端口 $OCTO_PORT 已被其他程序占用，请换 OCTO_PORT"
  fi
fi

# ---------------------------------------------------------------- 0. 登录账号
AUTH_DIR=/etc/octo-auth
SET_CRED=1
if [ -z "${OCTO_ADMIN_USER:-}" ] || [ -z "${OCTO_ADMIN_PASS:-}" ]; then
  [ -r /dev/tty ] || die "非交互环境请用 OCTO_ADMIN_USER / OCTO_ADMIN_PASS 指定登录账号密码"
  if [ -f "$AUTH_DIR/auth.json" ]; then
    read -r -p "已有登录账号，是否保留？[Y/n] " keep </dev/tty
    case "${keep:-Y}" in [Nn]*) ;; *) SET_CRED=0 ;; esac
  fi
  if [ "$SET_CRED" = 1 ]; then
    read -r -p "设置管理员账号 [admin]: " OCTO_ADMIN_USER </dev/tty
    OCTO_ADMIN_USER="${OCTO_ADMIN_USER:-admin}"
    while :; do
      read -r -s -p "设置管理员密码: " OCTO_ADMIN_PASS </dev/tty; echo
      read -r -s -p "再输入一次: " p2 </dev/tty; echo
      [ -n "$OCTO_ADMIN_PASS" ] && [ "$OCTO_ADMIN_PASS" = "$p2" ] && break
      warn "两次不一致或为空，请重新输入"
    done
    [ "${#OCTO_ADMIN_PASS}" -ge 10 ] || warn "密码较短，放公网建议 10 位以上"
  fi
fi

# ---------------------------------------------------------------- 1. 依赖
log "安装依赖..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl tar ca-certificates openssl git ripgrep python3 >/dev/null
if ! dpkg -s nginx-light >/dev/null 2>&1 && ! dpkg -s nginx >/dev/null 2>&1; then
  apt-get install -y -qq nginx-light >/dev/null 2>&1 || apt-get install -y -qq nginx >/dev/null
fi
# 不要 Debian 默认站点占 80 端口
rm -f /etc/nginx/sites-enabled/default

# ---------------------------------------------------------------- 2. swap
if [ "$SWAP_TARGET_MB" -gt 0 ]; then
  cur_swap=$(free -m | awk '/^Swap:/{print $2}')
  if [ "$cur_swap" -lt $((SWAP_TARGET_MB - 128)) ]; then
    add=$((SWAP_TARGET_MB - cur_swap))
    if [ ! -f /swapfile.octo ]; then
      log "swap 当前 ${cur_swap}M，新增 ${add}M 到约 ${SWAP_TARGET_MB}M..."
      fallocate -l "${add}M" /swapfile.octo 2>/dev/null || dd if=/dev/zero of=/swapfile.octo bs=1M count="$add" status=none
      chmod 600 /swapfile.octo
      mkswap /swapfile.octo >/dev/null
      swapon /swapfile.octo
      grep -q '/swapfile.octo' /etc/fstab || echo '/swapfile.octo none swap sw 0 0' >> /etc/fstab
    fi
  fi
  # 小内存机器：少用 swap，但内存紧张时可以顶上
  echo 'vm.swappiness=20' > /etc/sysctl.d/90-octo-swap.conf
  sysctl -q -p /etc/sysctl.d/90-octo-swap.conf || true
fi

# ---------------------------------------------------------------- 3. octo
log "安装/升级 octo..."
# 优先从自己的镜像仓库下载（防上游删库），失败再回退官方安装脚本
case "$(uname -m)" in x86_64|amd64) OCTO_ARCH=amd64 ;; aarch64|arm64) OCTO_ARCH=arm64 ;; *) die "不支持的架构 $(uname -m)" ;; esac
install_from_mirror() {
  local base="https://github.com/$OCTO_REPO" ver="${OCTO_VERSION:-}" tmp
  if [ -z "$ver" ]; then
    ver=$(curl -fsS --max-time 20 -o /dev/null -w '%{redirect_url}' "$base/releases/latest" | sed -n 's#.*/releases/tag/v\{0,1\}\([^/?]*\).*#\1#p')
  fi
  [ -n "$ver" ] || return 1
  tmp=$(mktemp -d)
  local a="octo_${ver}_linux_${OCTO_ARCH}.tar.gz"
  curl -fsSL --max-time 300 -o "$tmp/$a" "$base/releases/download/v$ver/$a" \
    && curl -fsSL --max-time 30 -o "$tmp/checksums.txt" "$base/releases/download/v$ver/checksums.txt" \
    && (cd "$tmp" && grep " $a\$" checksums.txt | sha256sum -c --quiet -) \
    && tar -xzf "$tmp/$a" -C "$tmp" octo \
    && install -m 755 "$tmp/octo" /usr/local/bin/octo
  local rc=$?; rm -rf "$tmp"; return $rc
}
if install_from_mirror; then
  log "已从镜像仓库 $OCTO_REPO 安装"
else
  warn "镜像仓库 $OCTO_REPO 下载失败，改用官方安装脚本"
  curl -fsSL https://octo-agent.dev/install.sh -o /tmp/octo-install.sh \
    || curl -fsSL https://dl.octo-agent.dev/install.sh -o /tmp/octo-install.sh \
    || die "下载 octo 失败"
  OCTO_INSTALL_DIR=/usr/local/bin sh /tmp/octo-install.sh >/dev/null
  rm -f /tmp/octo-install.sh
fi
log "$(/usr/local/bin/octo --version)"

# ---------------------------------------------------------------- 4. 用户
if ! id "$OCTO_USER" >/dev/null 2>&1; then
  log "创建用户 $OCTO_USER..."
  useradd -m -s /bin/bash "$OCTO_USER"
fi
OCTO_HOME=$(getent passwd "$OCTO_USER" | cut -d: -f6)
install -d -m 700 -o "$OCTO_USER" -g "$OCTO_USER" "$OCTO_HOME/.octo" "$OCTO_HOME/workspace"

# 访问密钥：沿用已有的，否则随机生成
ENV_FILE="$OCTO_HOME/.octo/serve.env"
if [ -z "${OCTO_ACCESS_KEY:-}" ] && [ -f "$ENV_FILE" ]; then
  OCTO_ACCESS_KEY=$(sed -n 's/^OCTO_ACCESS_KEY=//p' "$ENV_FILE" | head -n1)
fi
OCTO_ACCESS_KEY="${OCTO_ACCESS_KEY:-$(openssl rand -hex 24)}"
touch "$ENV_FILE"
sed -i '/^OCTO_ACCESS_KEY=/d' "$ENV_FILE"
echo "OCTO_ACCESS_KEY=$OCTO_ACCESS_KEY" >> "$ENV_FILE"
chown "$OCTO_USER:$OCTO_USER" "$ENV_FILE"; chmod 600 "$ENV_FILE"

# 预置免 key 搜索（只在没有 mcp.json 时写入，不覆盖用户自己的配置）
MCP_FILE="$OCTO_HOME/.octo/mcp.json"
if [ ! -s "$MCP_FILE" ]; then
  cat > "$MCP_FILE" <<'EOF'
{
  "mcpServers": {
    "keenable": {
      "url": "https://api.keenable.ai/mcp"
    },
    "parallel": {
      "url": "https://mcp.parallel.ai/mcp"
    }
  }
}
EOF
  chown "$OCTO_USER:$OCTO_USER" "$MCP_FILE"; chmod 644 "$MCP_FILE"
fi

# ---------------------------------------------------------------- 5. systemd
log "配置 systemd 服务..."
cat > /etc/systemd/system/octo.service <<EOF
[Unit]
Description=octo-agent (octo serve)
After=network-online.target
Wants=network-online.target

[Service]
User=$OCTO_USER
Group=$OCTO_USER
WorkingDirectory=$OCTO_HOME/workspace
Environment=HOME=$OCTO_HOME
EnvironmentFile=$ENV_FILE
ExecStart=/usr/local/bin/octo serve -addr $INTERNAL_ADDR --no-supervisor
Restart=always
RestartSec=3
# 1G 小鸡保护：octo 吃太多时先回收，再超就杀掉重启，不拖垮 v2ray/ssh
MemoryHigh=550M
MemoryMax=750M
OOMScoreAdjust=300

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable octo >/dev/null 2>&1
systemctl restart octo

# ---------------------------------------------------------------- 6. 登录服务
log "配置登录服务..."
id octo-auth >/dev/null 2>&1 || useradd -r -s /usr/sbin/nologin -M octo-auth
install -d -m 750 -o root -g octo-auth "$AUTH_DIR"
cat > /usr/local/bin/octo-auth <<'OCTOAUTH_EOF'
#!/usr/bin/env python3
"""octo-auth: 给 octo 网页加一层账号密码登录（nginx auth_request 后端，仅用标准库）。"""
import hashlib, hmac, html, json, os, secrets, time, urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

CONF = os.environ.get("OCTO_AUTH_CONF", "/etc/octo-auth/auth.json")
LISTEN = ("127.0.0.1", int(os.environ.get("OCTO_AUTH_PORT", "8090")))
COOKIE = "octo_session"
SESSION_DAYS = 365
MAX_FAILS, LOCK_SECS = 5, 900

with open(CONF) as f:
    cfg = json.load(f)
SECRET = bytes.fromhex(cfg["secret"])
fails = {}  # ip -> [count, first_ts]


def check_password(user, pw):
    ok_user = hmac.compare_digest(user.encode(), cfg["user"].encode())
    dk = hashlib.pbkdf2_hmac("sha256", pw.encode(), bytes.fromhex(cfg["salt"]), cfg["iter"])
    return ok_user & hmac.compare_digest(dk.hex(), cfg["hash"])


def make_token(user):
    exp = str(int(time.time()) + SESSION_DAYS * 86400)
    payload = f"{user}|{exp}|{cfg['hash'][:8]}"  # 改密码后旧会话自动失效
    sig = hmac.new(SECRET, payload.encode(), hashlib.sha256).hexdigest()
    return urllib.parse.quote(f"{payload}|{sig}")


def valid_token(raw):
    try:
        user, exp, ver, sig = urllib.parse.unquote(raw).split("|")
    except ValueError:
        return False
    good = hmac.new(SECRET, f"{user}|{exp}|{ver}".encode(), hashlib.sha256).hexdigest()
    return (hmac.compare_digest(sig, good) and int(exp) > time.time()
            and user == cfg["user"] and ver == cfg["hash"][:8])


PAGE = """<!doctype html><html lang="zh"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1"><title>octo 登录</title>
<style>body{{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;
font-family:system-ui,-apple-system,sans-serif;background:#0f1115;color:#e6e6e6}}
form{{width:min(88vw,340px);background:#181b22;padding:28px;border-radius:14px;box-shadow:0 8px 30px #0006}}
h1{{margin:0 0 18px;font-size:20px}}input{{width:100%;box-sizing:border-box;margin:6px 0 14px;padding:12px;
border-radius:8px;border:1px solid #333;background:#0f1115;color:#fff;font-size:16px}}
button{{width:100%;padding:12px;border:0;border-radius:8px;background:#4f6ef7;color:#fff;font-size:16px}}
.err{{color:#ff6b6b;margin-bottom:10px;font-size:14px}}</style></head><body>
<form method="post" action="/login"><h1>octo 登录</h1>{err}
<input type="hidden" name="next" value="{nxt}">
<label>账号<input name="user" autocomplete="username" required autofocus></label>
<label>密码<input name="pw" type="password" autocomplete="current-password" required></label>
<button type="submit">登录</button></form></body></html>"""


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def ip(self):
        return self.headers.get("X-Real-IP", self.client_address[0])

    def cookie(self):
        for part in self.headers.get("Cookie", "").split(";"):
            k, _, v = part.strip().partition("=")
            if k == COOKIE:
                return v
        return ""

    def send(self, code, body=b"", headers=()):
        self.send_response(code)
        for k, v in headers:
            self.send_header(k, v)
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(body)

    def page(self, err="", nxt="/"):
        e = f'<div class="err">{html.escape(err)}</div>' if err else ""
        body = PAGE.format(err=e, nxt=html.escape(nxt, quote=True)).encode()
        self.send(200, body, [("Content-Type", "text/html; charset=utf-8")])

    def do_GET(self):
        u = urllib.parse.urlparse(self.path)
        if u.path == "/auth":
            return self.send(204 if valid_token(self.cookie()) else 401)
        if u.path == "/login":
            nxt = urllib.parse.parse_qs(u.query).get("next", ["/"])[0]
            if valid_token(self.cookie()):
                return self.send(302, headers=[("Location", safe_next(nxt))])
            return self.page(nxt=nxt)
        if u.path == "/logout":
            return self.send(302, headers=[("Location", "/login"),
                ("Set-Cookie", f"{COOKIE}=; Path=/; Max-Age=0; Secure; HttpOnly; SameSite=Lax")])
        self.send(404)

    def do_POST(self):
        if urllib.parse.urlparse(self.path).path != "/login":
            return self.send(404)
        n = min(int(self.headers.get("Content-Length", 0) or 0), 4096)
        form = urllib.parse.parse_qs(self.rfile.read(n).decode(errors="ignore"))
        user, pw = form.get("user", [""])[0], form.get("pw", [""])[0]
        nxt = form.get("next", ["/"])[0]
        ip, now = self.ip(), time.time()
        c = fails.get(ip)
        if c and now - c[1] > LOCK_SECS:
            fails.pop(ip, None); c = None
        if c and c[0] >= MAX_FAILS:
            return self.page("尝试次数过多，请 15 分钟后再试", nxt)
        if check_password(user, pw):
            fails.pop(ip, None)
            return self.send(302, headers=[("Location", safe_next(nxt)),
                ("Set-Cookie", f"{COOKIE}={make_token(user)}; Path=/; Max-Age={SESSION_DAYS*86400}; "
                               "Secure; HttpOnly; SameSite=Lax")])
        if c:
            c[0] += 1
        else:
            fails[ip] = [1, now]
        time.sleep(1)
        self.page("账号或密码错误", nxt)


def safe_next(nxt):
    return nxt if nxt.startswith("/") and not nxt.startswith("//") else "/"


if __name__ == "__main__":
    ThreadingHTTPServer(LISTEN, H).serve_forever()
OCTOAUTH_EOF
chmod 755 /usr/local/bin/octo-auth
if [ "$SET_CRED" = 1 ]; then
  OCTO_ADMIN_USER="$OCTO_ADMIN_USER" OCTO_ADMIN_PASS="$OCTO_ADMIN_PASS" python3 - "$AUTH_DIR/auth.json" <<'PY'
import hashlib, json, os, secrets, sys
pw = os.environ["OCTO_ADMIN_PASS"].encode()
try:  # 账号密码没变就保持原样，已登录的浏览器不会被踢下线
    old = json.load(open(sys.argv[1]))
    if old["user"] == os.environ["OCTO_ADMIN_USER"] and \
       hashlib.pbkdf2_hmac("sha256", pw, bytes.fromhex(old["salt"]), old["iter"]).hex() == old["hash"]:
        sys.exit(0)
except (OSError, ValueError, KeyError):
    pass
salt = secrets.token_bytes(16); it = 300000
json.dump({"user": os.environ["OCTO_ADMIN_USER"], "salt": salt.hex(), "iter": it,
           "hash": hashlib.pbkdf2_hmac("sha256", pw, salt, it).hex(),
           "secret": secrets.token_hex(32)}, open(sys.argv[1], "w"))
PY
  chown root:octo-auth "$AUTH_DIR/auth.json"; chmod 640 "$AUTH_DIR/auth.json"
fi
unset OCTO_ADMIN_PASS p2
cat > /etc/systemd/system/octo-auth.service <<EOF
[Unit]
Description=octo login gate
After=network.target

[Service]
User=octo-auth
ExecStart=/usr/bin/python3 /usr/local/bin/octo-auth
Restart=always
RestartSec=2
MemoryMax=64M
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable octo-auth >/dev/null 2>&1
systemctl restart octo-auth

# ---------------------------------------------------------------- 7. TLS + nginx
if [ -z "${OCTO_HOST:-}" ]; then
  OCTO_HOST=$(curl -4 -fsS --max-time 5 https://api.ipify.org 2>/dev/null \
           || curl -4 -fsS --max-time 5 https://ifconfig.me 2>/dev/null \
           || hostname -I | awk '{print $1}')
fi
CERT_DIR=/etc/nginx/octo-ssl
mkdir -p "$CERT_DIR"
if [ ! -f "$CERT_DIR/cert.pem" ] || ! openssl x509 -in "$CERT_DIR/cert.pem" -noout -text | grep -q "$OCTO_HOST"; then
  log "生成自签名证书（$OCTO_HOST，10 年）..."
  if [[ "$OCTO_HOST" =~ ^[0-9.]+$ || "$OCTO_HOST" == *:* ]]; then SAN="IP:$OCTO_HOST"; else SAN="DNS:$OCTO_HOST"; fi
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -sha256 \
    -days 3650 -subj "/CN=$OCTO_HOST" -addext "subjectAltName=$SAN" \
    -keyout "$CERT_DIR/key.pem" -out "$CERT_DIR/cert.pem" 2>/dev/null
  chmod 600 "$CERT_DIR/key.pem"
fi

log "配置 nginx（端口 $OCTO_PORT）..."
cat > /etc/nginx/conf.d/octo.conf <<EOF
map \$http_upgrade \$octo_conn_upgrade { default upgrade; '' close; }

server {
    listen $OCTO_PORT ssl http2;
    listen [::]:$OCTO_PORT ssl http2;
    server_name _;

    ssl_certificate     $CERT_DIR/cert.pem;
    ssl_certificate_key $CERT_DIR/key.pem;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:octo:1m;

    # 同端口误用 http 时跳到 https
    error_page 497 =301 https://\$host:\$server_port\$request_uri;

    client_max_body_size 50m;

    # 登录页（不需要登录即可访问）
    location = /login  { proxy_pass http://127.0.0.1:8090; proxy_set_header X-Real-IP \$remote_addr; }
    location = /logout { proxy_pass http://127.0.0.1:8090; }
    location = /__octo_auth {
        internal;
        proxy_pass http://127.0.0.1:8090/auth;
        proxy_pass_request_body off;
        proxy_set_header Content-Length "";
        proxy_set_header Cookie \$http_cookie;
    }
    location @octo_login { return 302 /login?next=\$request_uri; }

    location / {
        auth_request /__octo_auth;
        error_page 401 = @octo_login;

        proxy_pass http://$INTERNAL_ADDR;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        # 必须覆盖（不能追加）：octo 据此判断非本机请求并强制校验访问密钥
        proxy_set_header X-Forwarded-For \$remote_addr;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-Proto https;
        # 登录通过后由 nginx 代填 octo 访问密钥，浏览器端无需知道密钥
        proxy_set_header Authorization "Bearer $OCTO_ACCESS_KEY";
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$octo_conn_upgrade;
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
EOF
chmod 600 /etc/nginx/conf.d/octo.conf
# 小内存：1 个 worker 足够
sed -i 's/^worker_processes .*/worker_processes 1;/' /etc/nginx/nginx.conf
nginx -t -q
systemctl enable nginx >/dev/null 2>&1
systemctl restart nginx

# ---------------------------------------------------------------- 8. 自检
sleep 3
systemctl is-active --quiet octo  || die "octo 没有启动，查看：journalctl -u octo -n 50"
systemctl is-active --quiet nginx || die "nginx 没有启动，查看：journalctl -u nginx -n 50"
systemctl is-active --quiet octo-auth || die "登录服务没有启动，查看：journalctl -u octo-auth -n 50"
code_nologin=$(curl -sk -o /dev/null -w '%{http_code}' "https://127.0.0.1:$OCTO_PORT/api/sessions")
code_direct=$(curl -s -o /dev/null -w '%{http_code}' -H 'X-Forwarded-For: 203.0.113.1' "http://$INTERNAL_ADDR/api/sessions")
[ "$code_nologin" = "302" ] || warn "警告：未登录访问返回 $code_nologin（应为 302 跳登录页）"
[ "$code_direct" = "401" ]  || warn "警告：octo 访问密钥校验异常（$code_direct）"
if command -v ufw >/dev/null && ufw status | grep -q active; then
  ufw allow "$OCTO_PORT/tcp" >/dev/null && log "ufw 已放行 $OCTO_PORT"
fi

cat <<EOF

=====================================================================
 octo 部署完成
 访问地址：https://$OCTO_HOST:$OCTO_PORT
 用安装时设置的管理员账号密码登录；退出登录：/logout

 自签名证书，浏览器首次会提示"不安全"，选择继续访问即可。
 打开后在设置里选模型、填 API key。云厂商安全组记得放行 TCP $OCTO_PORT。

 常用命令：
   systemctl status octo        查看状态
   journalctl -u octo -f        看日志
   bash $0                      升级 octo / 重写配置 / 改登录密码（数据保留）
   journalctl -u octo-auth -f   看登录服务日志
=====================================================================
EOF
