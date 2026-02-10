# QuData Agent

Агент управления GPU-инстансами. Разворачивает Docker-контейнеры с GPU, управляет их жизненным циклом и выставляет все сервисы наружу через FRP-туннели.

## Деплой

```bash
curl -fsSL https://raw.githubusercontent.com/qubu-group/qudata-agent/main/scripts/install.py | sudo python3 - ak-YOUR-API-KEY
```

С готовым бинарником (без компиляции на хосте):

```bash
curl -fsSL https://raw.githubusercontent.com/qubu-group/qudata-agent/main/scripts/install.py -o /tmp/install.py
sudo python3 /tmp/install.py ak-YOUR-API-KEY --binary /path/to/qudata-agent
```

Удаление:

```bash
sudo python3 /opt/qudata-agent/scripts/uninstall.py
```

## Как работает

```
Qudata API                  FRP Server (*.qudata.ai)
    │                              │
    │ /init → secret + frp creds   │
    ▼                              │
┌─────────────────────────────────────────────┐
│  Agent (127.0.0.1:random)                   │
│                                             │
│  1. Стартует на случайном порту             │
│  2. Авторизуется в API → получает secret    │
│     и реквизиты FRP-сервера                 │
│  3. Генерирует frpc.toml, запускает FRPC    │
│  4. HTTP API агента доступен через FRP      │
│  5. Каждые 500мс отправляет метрики в API   │
│                                             │
│  При создании VM:                           │
│  ─ docker run --gpus=all ...                │
│  ─ Порты маппятся на 127.0.0.1             │
│  ─ FRPC конфиг обновляется новыми прокси    │
│  ─ FRPC перезапускается                     │
│  ─ Сервисы VM доступны только через FRP     │
└─────────────────────────────────────────────┘
```

**Извне доступно:**
- SSH: `ssh -p 10001 user@subdomain.ru1.qudata.ai`
- HTTP: `https://subdomain.ru1.qudata.ai:20000`
- API агента: `https://subdomain.ru1.qudata.ai`

**Напрямую — ничего.** Все порты слушают `127.0.0.1`, наружу выходят только через FRP.

## GPU в контейнере

GPU доступен внутри контейнера полностью. Контейнер запускается с:

```
--gpus=all
-e NVIDIA_VISIBLE_DEVICES=all
-e NVIDIA_DRIVER_CAPABILITIES=compute,utility
```

Это обеспечивает NVIDIA Container Toolkit. Внутри контейнера работает `nvidia-smi`, CUDA, PyTorch, TensorFlow — всё, что требует GPU.

**Требование:** на хосте должен быть установлен NVIDIA-драйвер и `nvidia-container-toolkit`. Скрипт установки ставит toolkit автоматически.

## Безопасность

| Мера | Как реализовано |
|------|-----------------|
| Авторизация API | `X-Agent-Secret` — секрет выдаётся при `/init`, сравнение constant-time |
| Сетевая изоляция | Все порты на `127.0.0.1` — прямого доступа к хосту нет |
| FRP-туннель | Аутентификация по token, subdomain уникален для агента |
| Файлы состояния | Права `0600` — agent_id, secret, api_key |
| SSH в VM | Только по ключу (PasswordAuthentication no), ключи добавляются через API |
| Docker | `--init` (корректная обработка сигналов), `--restart=unless-stopped` |
| NVML | dlopen в runtime — бинарник не падает без драйверов |

## API

Все запросы через FRP-туннель. Заголовок: `X-Agent-Secret: sk-...`

| Метод | Путь | Описание |
|-------|------|----------|
| `GET` | `/ping` | Проверка связи (без авторизации) |
| `POST` | `/instances` | Создать VM |
| `GET` | `/instances` | Статус VM |
| `PUT` | `/instances` | Управление: `start` / `stop` / `restart` |
| `DELETE` | `/instances` | Удалить VM |
| `POST` | `/ssh` | Добавить SSH-ключ |
| `DELETE` | `/ssh` | Удалить SSH-ключ |

### Создание VM

```json
POST /instances
{
  "image": "pytorch/pytorch:2.1.0-cuda12.1-cudnn8-runtime",
  "ports": [
    {"container_port": 22, "remote_port": 10001, "proto": "tcp"},
    {"container_port": 8888, "remote_port": 20000, "proto": "http"}
  ],
  "env_variables": {"JUPYTER_TOKEN": "secret"},
  "command": "jupyter lab --ip=0.0.0.0 --allow-root",
  "ssh_enabled": true
}
```

## Сборка

```bash
# В Docker (с любой ОС):
make extract              # бинарник → build/qudata-agent

# Локально (Linux + GCC):
make build
```

Бинарник собирается с CGO для dlopen. NVML-заголовки при сборке не нужны — `libnvidia-ml.so.1` загружается в runtime через `dlopen`.

## Переменные окружения

| Переменная | Обязательна | По умолчанию | Описание |
|------------|-------------|--------------|----------|
| `QUDATA_API_KEY` | Да | — | API-ключ (`ak-...`) |
| `QUDATA_SERVICE_URL` | Нет | `https://internal.qudata.ai/v0` | URL API |
| `QUDATA_AGENT_DEBUG` | Нет | `false` | Мок-режим без GPU |
| `QUDATA_FRPC_BINARY` | Нет | `/usr/local/bin/frpc` | Путь к frpc |

## Управление

```bash
systemctl status qudata-agent
systemctl restart qudata-agent
journalctl -u qudata-agent -f
```
