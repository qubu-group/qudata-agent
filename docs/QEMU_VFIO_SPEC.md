# QEMU VM с VFIO GPU Passthrough

## Архитектура

```
┌──────────────────────────────────────────────┐
│                  Хост                         │
│  ┌──────────────────────────────────────────┐ │
│  │            qudata-agent                  │ │
│  │  - Создаёт VM по запросу                 │ │
│  │  - GPU bind при создании, unbind при     │ │
│  │    удалении                              │ │
│  │  - SSH/QMP управление VM                 │ │
│  └──────────────────────────────────────────┘ │
│                    │                          │
│            VFIO passthrough                   │
│                    ▼                          │
│  ┌──────────────────────────────────────────┐ │
│  │             Ubuntu VM                    │ │
│  │  ┌──────────────────────────────────┐    │ │
│  │  │    Пользовательские приложения   │    │ │
│  │  │    (JupyterLab, training, etc)   │    │ │
│  │  └──────────────────────────────────┘    │ │
│  │             GPU (VFIO)                   │ │
│  └──────────────────────────────────────────┘ │
└──────────────────────────────────────────────┘
```

## Установка

```bash
curl -fsSL https://raw.githubusercontent.com/qubu-group/qudata-agent/main/install.sh | sudo bash -s -- ak-YOUR-API-KEY
```

Установщик:
1. Определяет NVIDIA GPU
2. Настраивает IOMMU в GRUB
3. Скачивает Ubuntu cloud image
4. Устанавливает NVIDIA driver + SSH в образ
5. Перезагружает систему
6. Запускает агент

## Компоненты

### internal/qemu/

- `manager.go` — lifecycle VM: Create/Stop/Manage/Status
- `ssh.go` — SSH клиент для VM
- `vfio.go` — VFIO bind/unbind GPU
- `monitor.go` — QMP протокол
- `recovery.go` — поиск и убийство orphan VM

### internal/frpc/

- `config.go` — генерация frpc.toml с TCP/HTTP прокси
- `process.go` — управление процессом frpc

## Переменные окружения

| Переменная | Описание |
|-----------|----------|
| `QUDATA_API_KEY` | API ключ |
| `QUDATA_GPU_PCI_ADDRS` | PCI адреса GPU |
| `QUDATA_BASE_IMAGE` | Путь к образу VM |
| `QUDATA_DEBUG` | Debug mode без GPU |
