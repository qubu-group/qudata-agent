# Настройка frpc клиента

В контексте **QuData Agent**: туннель создаётся сразу при создании агента (ответ API `/init` возвращает параметры FRP; агент поднимает frpc). Обращение к агенту извне идёт только по этому туннелю (по subdomain); локально агент слушает на `127.0.0.1`.

## Установка

```bash
FRP_VERSION=0.61.1
curl -fsSL "https://github.com/fatedier/frp/releases/download/v${FRP_VERSION}/frp_${FRP_VERSION}_linux_amd64.tar.gz" | tar -xz
sudo mv frp_${FRP_VERSION}_linux_amd64/frpc /usr/local/bin/
rm -rf frp_${FRP_VERSION}_linux_amd64
```

## Архитектура портов

| Диапазон | Тип | Маршрутизация | customDomains |
|----------|-----|---------------|---------------|
| 80, 443 | HTTP | по домену | `["secret"]` |
| 15001-65535 | HTTP | по домену + порт | `["secret:PORT"]` |
| 10000-15000 | TCP | по порту (уникальный) | — |

## Конфигурация

Создайте `frpc.toml`:

```toml
serverAddr = "agent.REGION.qudata.ai"
serverPort = 7000

[auth]
method = "token"
token = "YOUR_TOKEN"

# HTTP на стандартных портах (80, 443)
[[proxies]]
name = "web"
type = "http"
customDomains = ["YOUR_SECRET"]
localIP = "127.0.0.1"
localPort = 8080

# HTTP на кастомном порту (15001-65535)
[[proxies]]
name = "jupyter"
type = "http"
customDomains = ["YOUR_SECRET:20000"]
localIP = "127.0.0.1"
localPort = 8888

# TCP туннель (10000-15000)
[[proxies]]
name = "ssh"
type = "tcp"
localIP = "127.0.0.1"
localPort = 22
remotePort = 10001
```

## Типы прокси

### HTTP на стандартных портах (80, 443)

Несколько клиентов могут использовать одинаковые порты с разными доменами.

```toml
[[proxies]]
name = "api"
type = "http"
customDomains = ["mysecret"]
localPort = 8080
```

Доступ:
- `http://mysecret.ru1.qudata.ai`
- `https://mysecret.ru1.qudata.ai`

### HTTP на кастомных портах (15001-65535)

Порт включается в customDomains. Несколько клиентов могут использовать один порт с разными доменами.

```toml
[[proxies]]
name = "jupyter"
type = "http"
customDomains = ["mysecret:20000"]
localPort = 8888
```

Доступ: `https://mysecret.ru1.qudata.ai:20000`

### TCP (10000-15000)

Для SSH и raw TCP. Порт уникален — не может дублироваться между клиентами.

```toml
[[proxies]]
name = "ssh"
type = "tcp"
localPort = 22
remotePort = 10001
```

Доступ: `ssh -p 10001 user@mysecret.ru1.qudata.ai`

## Примеры

### Один сервис на стандартном порту

```toml
serverAddr = "agent.ru1.qudata.ai"
serverPort = 7000

[auth]
method = "token"
token = "xxx"

[[proxies]]
name = "ollama"
type = "http"
customDomains = ["mynode"]
localPort = 11434
```

Доступ: `https://mynode.ru1.qudata.ai`

### Несколько сервисов на разных портах

```toml
serverAddr = "agent.ru1.qudata.ai"
serverPort = 7000

[auth]
method = "token"
token = "xxx"

# API на стандартном порту
[[proxies]]
name = "api"
type = "http"
customDomains = ["mynode"]
localPort = 8080

# Jupyter на порту 20000
[[proxies]]
name = "jupyter"
type = "http"
customDomains = ["mynode:20000"]
localPort = 8888

# Grafana на порту 20001
[[proxies]]
name = "grafana"
type = "http"
customDomains = ["mynode:20001"]
localPort = 3000

# SSH
[[proxies]]
name = "ssh"
type = "tcp"
localPort = 22
remotePort = 10001
```

Доступ:
- API: `https://mynode.ru1.qudata.ai`
- Jupyter: `https://mynode.ru1.qudata.ai:20000`
- Grafana: `https://mynode.ru1.qudata.ai:20001`
- SSH: `ssh -p 10001 user@mynode.ru1.qudata.ai`

## Запуск

```bash
frpc -c frpc.toml
```

## Systemd

```bash
sudo mkdir -p /etc/frpc
sudo cp frpc.toml /etc/frpc/

sudo tee /etc/systemd/system/frpc.service << 'EOF'
[Unit]
Description=frpc tunnel client
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/frpc -c /etc/frpc/frpc.toml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable --now frpc
```
