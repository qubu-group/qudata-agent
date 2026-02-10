# QuData Agent

GPU-инстанс агент с QEMU/VFIO passthrough для эксклюзивного доступа к GPU.

## Архитектура

- **VM создаётся по запросу** при вызове `POST /instances` с GPU passthrough
- **GPU привязывается** к VM при создании, возвращается на хост при удалении
- **Туннель (FRP)** поднимается при старте агента; VM-порты добавляются при создании инстанса
- **При перезапуске** агента активная VM убивается, GPU возвращается на хост

### Порты

| Диапазон | Назначение | Тип |
|----------|-----------|-----|
| 10000-15000 | SSH к VM | TCP |
| 15001-65535 | Приложения | HTTP |

## Установка

```bash
curl -fsSL https://raw.githubusercontent.com/qubu-group/qudata-agent/main/install.sh | sudo bash -s -- ak-YOUR-API-KEY
```

Установщик:
1. Определяет NVIDIA GPU
2. Настраивает IOMMU/VFIO в GRUB
3. Скачивает Ubuntu cloud image, устанавливает NVIDIA driver
4. Перезагружает (если нужно), запускает агент

### Debug mode

```bash
curl -fsSL ... | sudo bash -s -- ak-YOUR-API-KEY --debug
```

## Требования

- Ubuntu/Debian
- NVIDIA GPU с поддержкой VFIO
- VT-d/AMD-Vi в BIOS
- 32 GB+ RAM
- 50 GB+ свободного места

## Конфигурация

| Переменная | Описание | По умолчанию |
|-----------|----------|--------------|
| `QUDATA_API_KEY` | API ключ | — |
| `QUDATA_GPU_PCI_ADDRS` | PCI адреса GPU | auto |
| `QUDATA_BASE_IMAGE` | Путь к образу VM | `/var/lib/qudata/images/qudata-base.qcow2` |
| `QUDATA_DEBUG` | Debug mode | `false` |

## Управление

```bash
systemctl status qudata-agent
journalctl -u qudata-agent -f
systemctl restart qudata-agent
```

## Структура

```
/var/lib/qudata/
├── images/           # qcow2 образы
├── .ssh/             # SSH ключи для VM
└── data/             # Данные инстансов

/var/run/qudata/      # QMP сокеты, runtime
/var/log/qudata/      # Логи
/etc/qudata/          # FRP конфигурация
```
